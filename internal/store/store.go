package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/heyjdp/vlt/internal/crypto"
)

// ErrSecretNotFound is returned when a secret does not exist in the vault.
var ErrSecretNotFound = errors.New("secret not found")

// ErrSecretExists is returned by Create when the secret's file is already
// taken, and by Update when it is held by a differently named secret. Both
// return it as an *ExistsError, which says what is there.
var ErrSecretExists = errors.New("a secret with that name already exists")

// ExistsError is the error Create and Update wrap when a secret's file is
// taken (see ErrSecretExists, which it matches with errors.Is). It carries
// what the file holds, so a caller can offer to replace that secret without
// reading the vault again, and without the file changing in between unseen
// (Update still refuses a file held by another name).
type ExistsError struct {
	// Existing is the secret stored in the file, or nil if the file
	// couldn't be read, decrypted or placed (see ReadErr).
	Existing *Secret
	// ReadErr says why the file couldn't be read when Existing is nil.
	ReadErr error
}

// Error returns ErrSecretExists's message, and why the file couldn't be
// read if it couldn't.
func (e *ExistsError) Error() string {
	if e.ReadErr != nil {
		return ErrSecretExists.Error() + "; its file can't be read: " + e.ReadErr.Error()
	}
	return ErrSecretExists.Error()
}

// Unwrap returns ErrSecretExists and, if the file couldn't be read, why.
func (e *ExistsError) Unwrap() []error {
	if e.ReadErr != nil {
		return []error{ErrSecretExists, e.ReadErr}
	}
	return []error{ErrSecretExists}
}

// Store manages secrets in an age-encrypted vault, in a local directory or
// on an SSH server. Secrets are stored at <group>/<slug>.age, encrypted to
// the vault key. Use Create or Open to obtain one. Its methods may be called
// concurrently.
type Store struct {
	fs  vaultFS
	loc Location
	id  *crypto.Identity
	log *slog.Logger

	opts options

	// skipped lists legacy files that could not be migrated (see migrate.go).
	skipped []string

	// lockMu guards lockToken, the token of the vault's lock file while this
	// Store holds it; elsewhere, the lock another copy of vlt held when this
	// Store was opened, until TakeOver; and closing, set once Close has
	// begun, after which no TakeOver starts (see lock.go).
	lockMu    sync.Mutex
	lockToken string
	elsewhere *LockInfo
	closing   bool
	// takeOvers counts the TakeOver calls running, which Close waits for
	// before releasing the lock.
	takeOvers sync.WaitGroup

	closeOnce sync.Once
	closed    chan struct{}
}

func newStore(id *crypto.Identity, fsys vaultFS, loc Location, log *slog.Logger, o options) *Store {
	return &Store{
		fs:     fsys,
		loc:    loc,
		id:     id,
		log:    log,
		opts:   o,
		closed: make(chan struct{}),
	}
}

// Dir returns the vault's location, in the form ParseLocation reads.
func (s *Store) Dir() string {
	return s.loc.String()
}

// Location returns where the vault lives.
func (s *Store) Location() Location {
	return s.loc
}

// Create encrypts and writes a new secret to vault/<group>/<slug>.age. It
// returns an *ExistsError (ErrSecretExists) holding the secret already
// there if any secret occupies that path, including a different name with
// the same SecretID (e.g. "GitHub Key" and "github-key"), so it never
// overwrites. The name must pass NewSecretName. The secret's Name is
// trimmed in place and its Group set to GroupLabel(Group).
func (s *Store) Create(ctx context.Context, secret *Secret) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	sn, err := NewSecretName(secret.Name)
	if err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	secret.Name = sn.String()
	name := s.path(secret.Name, secret.Group)

	data, err := s.seal(secret)
	if err != nil {
		return fmt.Errorf("store.Create: %w", err)
	}
	if err := s.fs.MkdirAll(path.Dir(name)); err != nil {
		return fmt.Errorf("store.Create: mkdir: %w", err)
	}

	err = s.fs.WriteFileExclusive(name, data)
	if errors.Is(err, fs.ErrExist) {
		existing, readErr := s.readSecret(name)
		if readErr != nil {
			s.log.Warn("secret file is taken and can't be read", "path", name, "err", readErr)
		}
		return fmt.Errorf("store.Create %q in %q: %w", secret.Name, secret.Group, &ExistsError{Existing: existing, ReadErr: readErr})
	}
	if err != nil {
		return fmt.Errorf("store.Create: write: %w", err)
	}

	s.log.Info("secret created", "name", secret.Name, "group", secret.Group)
	return nil
}

// Update re-encrypts an existing secret in place. The file at the secret's
// path must exist and hold a secret with the same name (compared after
// trimming, so names stored untrimmed by older versions still match);
// otherwise it returns ErrSecretNotFound or an *ExistsError holding the
// differently named secret that shares its SecretID. CreatedAt is
// preserved from the stored copy, and Group is set to GroupLabel(Group).
// Names that predate NewSecretName's rules are accepted here so legacy
// secrets stay editable.
func (s *Store) Update(ctx context.Context, secret *Secret) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
	name, err := lookupName(secret.Name)
	if err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
	existing, err := s.Get(ctx, name, secret.Group)
	if err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
	if strings.TrimSpace(existing.Name) != name {
		return fmt.Errorf("store.Update %q: %w (its file is held by %q)", name, &ExistsError{Existing: existing}, existing.Name)
	}
	secret.Name = name
	secret.CreatedAt = existing.CreatedAt

	data, err := s.seal(secret)
	if err != nil {
		return fmt.Errorf("store.Update: %w", err)
	}
	if err := s.fs.WriteFileAtomic(s.path(name, secret.Group), data); err != nil {
		return fmt.Errorf("store.Update: write: %w", err)
	}

	s.log.Info("secret updated", "name", secret.Name, "group", secret.Group)
	return nil
}

// seal sets the secret's group label, stamps its timestamps, and returns its
// ciphertext. The caller has already validated and trimmed the name.
func (s *Store) seal(secret *Secret) ([]byte, error) {
	secret.Group = GroupLabel(secret.Group)
	secret.ModifiedAt = time.Now().UTC()
	if secret.CreatedAt.IsZero() {
		secret.CreatedAt = secret.ModifiedAt
	}
	if secret.Version == 0 {
		secret.Version = 1
	}

	plaintext, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	ciphertext, err := crypto.Encrypt(s.id, plaintext)
	crypto.Wipe(plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt: %w", err)
	}
	return ciphertext, nil
}

// lookupName trims a name for locating an existing secret. Unlike
// NewSecretName it accepts names with an empty slug, which older versions
// stored at <group>/.age, so those secrets can still be opened, edited and
// deleted.
func lookupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("secret name cannot be empty")
	}
	if len(name) > 255 {
		return "", errors.New("secret name exceeds 255 bytes")
	}
	return name, nil
}

// path returns the vault-relative path of the secret named name in group;
// see SecretID.
func (s *Store) path(name, group string) string {
	return secretID(name, group).file()
}

// Get decrypts and returns a secret by name and group.
func (s *Store) Get(ctx context.Context, name, group string) (*Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("store.Get: %w", err)
	}
	name, err := lookupName(name)
	if err != nil {
		return nil, fmt.Errorf("store.Get: %w", err)
	}
	ciphertext, err := s.fs.ReadFile(s.path(name, group))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("store.Get %q: %w", name, ErrSecretNotFound)
		}
		return nil, fmt.Errorf("store.Get %q: %w", name, err)
	}

	secret, err := s.openSecret(s.path(name, group), ciphertext)
	if err != nil {
		return nil, fmt.Errorf("store.Get %q: %w", name, err)
	}
	return secret, nil
}

// Delete removes a secret from the vault by name and group.
func (s *Store) Delete(ctx context.Context, name, group string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.Delete: %w", err)
	}
	name, err := lookupName(name)
	if err != nil {
		return fmt.Errorf("store.Delete: %w", err)
	}
	if err := s.fs.Remove(s.path(name, group)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("store.Delete %q: %w", name, ErrSecretNotFound)
		}
		return fmt.Errorf("store.Delete %q: %w", name, err)
	}

	s.log.Info("secret deleted", "name", name, "group", NormalizeGroup(group))
	return nil
}

// listWorkers bounds how many secret files Contents reads at once. Reading in
// parallel hides the round trip to a server; locally it changes little.
const listWorkers = 8

// Contents is what Store.Contents found in a vault.
type Contents struct {
	// Secrets are the readable secrets, in directory order.
	Secrets []*Secret
	// Unreadable are the vault-relative paths of secret files (and group
	// folders) that could not be read, decrypted or parsed, or hold a secret
	// stored elsewhere (see openSecret), sorted. A group folder ends in "/".
	Unreadable []string
}

// Contents returns all secrets in the vault by walking every group folder
// (secrets in directory order; a vault kept inside this one is not a group),
// and names the files it skipped: those
// it cannot read, decrypt or parse are logged and left out, so one damaged
// file doesn't hide the rest. A lost connection fails the whole listing
// rather than leave secrets silently missing. It checks ctx before each file
// and returns ctx's error once it is cancelled.
func (s *Store) Contents(ctx context.Context) (Contents, error) {
	c, err := s.contents(ctx)
	if err != nil {
		return Contents{}, fmt.Errorf("store.Contents: %w", err)
	}
	return c, nil
}

func (s *Store) contents(ctx context.Context) (Contents, error) {
	var c Contents
	groups, err := groupFolders(s.fs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, err
	}

	var names []string
	for _, g := range groups {
		if g.err != nil {
			if errors.Is(g.err, ErrConnectionLost) || errors.Is(g.err, ErrUnreachable) {
				return c, g.err
			}
			s.log.Warn("failed to read group directory", "group", g.name, "err", g.err)
			c.Unreadable = append(c.Unreadable, g.name+"/")
			continue
		}
		names = append(names, g.files...)
	}

	// Each worker fills its own slots, so the result keeps directory order.
	found := make([]*Secret, len(names))
	failed := make([]error, len(names))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(listWorkers, len(names)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				found[i], failed[i] = s.readSecret(names[i])
			}
		}()
	}
	var cancelled error
	for i := range names {
		if cancelled = ctx.Err(); cancelled != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if cancelled != nil {
		return c, cancelled
	}
	// A lost connection would otherwise leave secrets silently missing from
	// the list; fail instead, so the user sees why.
	for _, err := range failed {
		if errors.Is(err, ErrConnectionLost) || errors.Is(err, ErrUnreachable) {
			return c, err
		}
	}

	for i, secret := range found {
		if failed[i] != nil {
			s.log.Warn("skipping unreadable secret file", "path", names[i], "err", failed[i])
			c.Unreadable = append(c.Unreadable, names[i])
			continue
		}
		c.Secrets = append(c.Secrets, secret)
	}
	slices.Sort(c.Unreadable)
	return c, nil
}

// readSecret reads, decrypts and checks one secret file for Contents.
func (s *Store) readSecret(name string) (*Secret, error) {
	ciphertext, err := s.fs.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return s.openSecret(name, ciphertext)
}

// openSecret decrypts and parses the secret file at file (vault-relative).
// Whoever can write the vault's files (a server, a shared folder) can't
// forge or alter one, since age authenticates each file, but could swap two
// or plant another vault's file. So the file must hold a secret, of a known
// type, whose name and group give back the path it was read from; a secret
// sharing its slug with another name (see Update) still matches.
func (s *Store) openSecret(file string, ciphertext []byte) (*Secret, error) {
	plaintext, err := crypto.Decrypt(s.id, ciphertext)
	if err != nil {
		return nil, err
	}
	var secret Secret
	err = json.Unmarshal(plaintext, &secret)
	crypto.Wipe(plaintext)
	if err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	switch secret.Type {
	case TypePassword, TypeRecord, TypeFile:
	default:
		return nil, fmt.Errorf("%w: unknown type %q", errInvalidSecret, secret.Type)
	}
	if strings.TrimSpace(secret.Name) == "" {
		return nil, fmt.Errorf("%w: no name", errInvalidSecret)
	}
	// Case is ignored: folders and files made by hand, or on a file system
	// that ignores case, may differ from the slug only in case, and no two
	// slugs differ only in case.
	if want := s.path(secret.Name, secret.Group); !strings.EqualFold(want, file) {
		return nil, fmt.Errorf("%w: it belongs at %s", errMisplaced, want)
	}
	return &secret, nil
}

// errInvalidSecret marks a file that decrypts and parses but holds no
// usable secret.
var errInvalidSecret = errors.New("file is not a valid secret")

// errMisplaced marks a secret file that holds a secret stored under another
// name or group.
var errMisplaced = errors.New("file holds a secret from another place in the vault")

// CopyTo copies the format marker, every group folder with its secret
// files, and last the wrapped vault key into the vault location dest
// (local, or on a server). A vault kept inside this one's folder is not one
// of its groups, so it is left behind. An open Store never has a migration
// in progress, so vlt.migrating.age is not copied, and the lock file belongs
// to this location, so it is not either.
//
// vlt.age goes last so that a copy stopped part-way (cancelled, or a lost
// connection) never looks like a whole vault: opening it reports
// ErrKeyMissing rather than showing a vault with secrets missing. Copying
// to dest again finishes such a copy (see resumable), unless this vault
// changed since (ErrStaleCopy). Any other location holding a vault or
// secret files is refused (ErrVaultExists), since overwriting another
// vault's files would make its secrets unrecoverable; so is one where
// another vault's vlt.age appears during the copy.
func (s *Store) CopyTo(ctx context.Context, dest string) error {
	dst, loc, err := openLocation(ctx, dest, s.opts)
	if err != nil {
		return fmt.Errorf("store.CopyTo: %w", err)
	}
	defer func() { _ = dst.Close() }()

	if err := s.copyTo(ctx, dst); err != nil {
		return fmt.Errorf("store.CopyTo %q: %w", loc, err)
	}
	s.log.Info("vault copied", "from", s.loc.String(), "to", loc.String())
	return nil
}

// copyTo does CopyTo's work on the opened destination dst.
func (s *Store) copyTo(ctx context.Context, dst vaultFS) error {
	done, err := s.checkCopyTarget(ctx, dst)
	if err != nil {
		return err
	}
	if err := dst.MkdirAll("."); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if !done[formatFile] {
		if err := s.copyFile(ctx, dst, formatFile); err != nil {
			return err
		}
	}
	if err := s.copySecrets(ctx, dst, done); err != nil {
		return err
	}
	return s.copyKey(ctx, dst)
}

// checkCopyTarget returns ErrVaultExists if dst holds a vault or secret
// files, unless it is an unfinished copy of this vault (see resumable); it
// then returns the files already copied, which need not be written again.
func (s *Store) checkCopyTarget(ctx context.Context, dst vaultFS) (done map[string]bool, err error) {
	exists, err := existsIn(dst)
	if err != nil || !exists {
		return nil, err
	}
	return s.resumable(ctx, dst)
}

// resumable returns the files of dst already copied if dst holds only an
// unfinished copy of this vault: no vlt.age and no migration, a format
// marker (if any) the same as this vault's, and every secret file byte for
// byte one of this vault's. Finishing such a copy overwrites nothing that
// isn't already here. A copy whose secret files are all this vault's but
// not all as they are now (changed or deleted since) is ErrStaleCopy;
// anything else is ErrVaultExists. It stops when ctx is cancelled, since
// over a slow connection it reads every file.
func (s *Store) resumable(ctx context.Context, dst vaultFS) (map[string]bool, error) {
	for _, name := range []string{keyFile, migratingFile} {
		if _, err := dst.Stat(name); err == nil {
			return nil, ErrVaultExists
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	paths, err := secretFiles(dst)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	done := make(map[string]bool, len(paths)+1)
	stale := false
	for _, name := range append(paths, formatFile) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, err := s.compareCopied(dst, name)
		if err != nil {
			return nil, err
		}
		switch state {
		case copiedSame:
			done[name] = true
		case copiedOutdated:
			stale = true
		case copiedForeign:
			return nil, ErrVaultExists
		case copiedMissing: // only the marker can be missing
		}
	}
	if stale {
		return nil, ErrStaleCopy
	}
	return done, nil
}

// copiedState is how a file at a copy's destination compares with this
// vault's.
type copiedState int

const (
	// copiedMissing is a file not at the destination.
	copiedMissing copiedState = iota
	// copiedSame is a file with the same bytes as this vault's.
	copiedSame
	// copiedOutdated is a secret file encrypted to this vault's key whose
	// bytes this vault no longer has: changed or deleted since.
	copiedOutdated
	// copiedForeign is any other file: not this vault's.
	copiedForeign
)

// compareCopied compares dst's file name with this vault's.
func (s *Store) compareCopied(dst vaultFS, name string) (copiedState, error) {
	theirs, err := dst.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return copiedMissing, nil
	}
	if err != nil {
		return 0, err
	}
	ours, err := s.fs.ReadFile(name)
	switch {
	case err == nil && bytes.Equal(ours, theirs):
		return copiedSame, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return 0, err
	case name == formatFile:
		return copiedForeign, nil
	}
	plaintext, err := crypto.Decrypt(s.id, theirs)
	if errors.Is(err, crypto.ErrIdentityZeroed) {
		return 0, err // closed (locked) meanwhile: no answer
	}
	if err != nil {
		return copiedForeign, nil //nolint:nilerr // a file this vault can't decrypt is not its own
	}
	crypto.Wipe(plaintext)
	return copiedOutdated, nil
}

// copyFile copies the vault-level file name to dst, if this vault has it.
func (s *Store) copyFile(ctx context.Context, dst vaultFS, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := s.fs.ReadFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if err := dst.WriteFileAtomic(name, data); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// copyKey copies vlt.age, if this vault has it, to dst. It never replaces
// one: another instance may have created a vault at dst since
// checkCopyTarget looked, and that vault's key must not be lost
// (ErrVaultExists).
func (s *Store) copyKey(ctx context.Context, dst vaultFS) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := s.fs.ReadFile(keyFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", keyFile, err)
	}
	err = dst.WriteFileExclusive(keyFile, data)
	if errors.Is(err, fs.ErrExist) {
		return ErrVaultExists
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", keyFile, err)
	}
	return nil
}

// copySecrets copies the group folders and their secret files to dst,
// except the files done (already there), stopping before the next file
// once ctx is cancelled.
func (s *Store) copySecrets(ctx context.Context, dst vaultFS, done map[string]bool) error {
	groups, err := groupFolders(s.fs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %q: %w", s.loc, err)
	}
	for _, g := range groups {
		if g.err != nil {
			return fmt.Errorf("read %q: %w", g.name, g.err)
		}
		if err := dst.MkdirAll(g.name); err != nil {
			return fmt.Errorf("mkdir %q: %w", g.name, err)
		}
		for _, name := range g.files {
			if done[name] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			data, err := s.fs.ReadFile(name)
			if err != nil {
				return fmt.Errorf("read file %q: %w", name, err)
			}
			if err := dst.WriteFileAtomic(name, data); err != nil {
				return fmt.Errorf("write file %q: %w", name, err)
			}
		}
	}
	return nil
}
