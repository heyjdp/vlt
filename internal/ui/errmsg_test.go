package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/update"
)

// codePrefix matches the "pkg.Func" prefixes that say where in the code an
// error came from; they must never reach people.
var codePrefix = regexp.MustCompile(`\b(store|crypto|update|totp)\.[A-Z]`)

// openTestVault creates a vault in a temporary folder and returns it open.
func openTestVault(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Create(context.Background(), dir, testPassphrase, quietLog(), testKDF)
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	t.Cleanup(s.Close)
	return s, dir
}

func TestErrorMessage_DescribesRealErrorsInPlainWords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name  string
		doing string
		err   func(t *testing.T) error
		want  string // how the message starts
	}{
		{
			name:  "wrong passphrase",
			doing: "Couldn't open the vault",
			err: func(t *testing.T) error {
				_, dir := openTestVault(t)
				_, err := store.Open(ctx, dir, "not the passphrase", quietLog(), testKDF)
				return err
			},
			want: "Couldn't open the vault: the passphrase is incorrect",
		},
		{
			name:  "a vault key asking for too much key-derivation work",
			doing: "Couldn't open the vault",
			err: func(t *testing.T) error {
				_, dir := openTestVault(t)
				key := filepath.Join(dir, "vlt.age")
				data, err := os.ReadFile(key)
				if err != nil {
					t.Fatal(err)
				}
				data = regexp.MustCompile(`(?m)^(-> scrypt \S+) \d+$`).ReplaceAll(data, []byte("$1 22"))
				if err := os.WriteFile(key, data, 0o600); err != nil {
					t.Fatal(err)
				}
				_, err = store.Open(ctx, dir, testPassphrase, quietLog(), testKDF)
				return err
			},
			want: "Couldn't open the vault: the passphrase-protected file asks for more key-derivation work than vlt allows (2^22; at most 2^20)",
		},
		{
			name:  "wrong current passphrase on change",
			doing: "Couldn't change the passphrase",
			err: func(t *testing.T) error {
				s, _ := openTestVault(t)
				return s.ChangePassphrase(ctx, "not the passphrase", "a new long passphrase")
			},
			want: "Couldn't change the passphrase: the passphrase is incorrect",
		},
		{
			name:  "no vault in the folder",
			doing: "Couldn't open the vault",
			err: func(t *testing.T) error {
				_, err := store.Open(ctx, t.TempDir(), testPassphrase, quietLog(), testKDF)
				return err
			},
			want: "Couldn't open the vault: vault not found",
		},
		{
			name:  "vault already there",
			doing: "Couldn't create the vault",
			err: func(t *testing.T) error {
				_, dir := openTestVault(t)
				_, err := store.Create(ctx, dir, testPassphrase, quietLog(), testKDF)
				return err
			},
			want: "Couldn't create the vault: vault already exists",
		},
		{
			name:  "secret already there",
			doing: "Couldn't save the secret",
			err: func(t *testing.T) error {
				s, _ := openTestVault(t)
				sec := &store.Secret{Version: 1, Type: store.TypePassword, Name: "GitHub", Group: "work", Value: "x"}
				if err := s.Create(ctx, sec); err != nil {
					t.Fatal(err)
				}
				return s.Create(ctx, &store.Secret{Version: 1, Type: store.TypePassword, Name: "github", Group: "Work", Value: "y"})
			},
			want: "Couldn't save the secret: a secret with that name already exists",
		},
		{
			name:  "vault open elsewhere",
			doing: "",
			err: func(t *testing.T) error {
				s, dir := openTestVault(t)
				plantLock(t, dir)
				err := s.AcquireLock(ctx, false)
				var inUse *store.InUseError
				if !errors.As(err, &inUse) {
					t.Fatalf("AcquireLock = %v, want an *InUseError", err)
				}
				return fmt.Errorf("store.Open: %w", err) // as callers may wrap it
			},
			want: "The vault is open on laptop.local (user me, process 4242) since 29 Oct 2026 09:30 UTC",
		},
		{
			name:  "bad location keeps its detail",
			doing: "",
			err: func(*testing.T) error {
				_, err := store.ParseLocation("nas:")
				return err
			},
			want: `Invalid vault location: "nas:" names no folder on the server`,
		},
		{
			name:  "cancelled",
			doing: "Couldn't open the vault",
			err: func(t *testing.T) error {
				s, _ := openTestVault(t)
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				return s.AcquireLock(cancelled, false)
			},
			want: "Couldn't open the vault: it was cancelled",
		},
		{
			name:  "too slow",
			doing: "Couldn't open the vault",
			err: func(t *testing.T) error {
				s, _ := openTestVault(t)
				expired, cancel := context.WithTimeout(ctx, 0)
				defer cancel()
				return s.AcquireLock(expired, false)
			},
			want: "Couldn't open the vault: it took too long",
		},
		{
			name:  "a failed move names both paths",
			doing: "",
			err: func(t *testing.T) error {
				dir := t.TempDir()
				return os.Rename(filepath.Join(dir, "gone"), filepath.Join(dir, "moved"))
			},
			want: "Can't move ",
		},
		{
			name:  "server error keeps what ssh said",
			doing: "",
			// As sshfs and Open wrap it: no server is needed to make one.
			err: func(*testing.T) error {
				return fmt.Errorf("store.Open %q: %w", "nas:/v",
					fmt.Errorf("%w %s: %s", store.ErrUnreachable, "nas", "ssh: connect to host nas port 22: Connection refused"))
			},
			want: "Cannot connect to the vault's server nas: ssh: connect to host nas port 22: Connection refused",
		},
		{
			name:  "update error by its own words",
			doing: "Couldn't check for updates",
			err: func(*testing.T) error {
				return fmt.Errorf("update.Latest: %w", update.ErrRateLimited)
			},
			want: "Couldn't check for updates: GitHub's request limit was reached; try again later",
		},
		{
			name:  "text after the cause is kept",
			doing: "Couldn't export \"key\"",
			err: func(*testing.T) error {
				write := &os.PathError{Op: "write", Path: "/tmp/key.pem", Err: errors.New("no space left on device")}
				return fmt.Errorf("%w (the file at %s may be incomplete)", fmt.Errorf("write privately: %w", write), "/tmp/key.pem")
			},
			want: `Couldn't export "key": can't write /tmp/key.pem: no space left on device (the file at /tmp/key.pem may be incomplete)`,
		},
		{
			name:  "a server file error keeps the file",
			doing: "Couldn't save the secret",
			// As the server backend reports it: pkg/sftp gives a bare
			// os.ErrPermission, which the backend puts in a PathError.
			err: func(*testing.T) error {
				return fmt.Errorf("store.Update: write: %w", &fs.PathError{Op: "write", Path: "work/x.age", Err: os.ErrPermission})
			},
			want: "Couldn't save the secret: can't write work/x.age: permission denied",
		},
		{
			name:  "a temp folder that can't be made private keeps its mode",
			doing: "Couldn't save the secret",
			err: func(*testing.T) error {
				private := fmt.Errorf("%w (%s has mode %v and cannot be made private)", os.ErrPermission, "/v/.vlt-tmp", fs.FileMode(0o777))
				return fmt.Errorf("store.Update: write: %w", &fs.PathError{Op: "write", Path: "work/x.age", Err: private})
			},
			want: "Couldn't save the secret: can't write work/x.age: permission denied (/v/.vlt-tmp has mode -rwxrwxrwx and cannot be made private)",
		},
		{
			name:  "a failed fallback after an unsupported link reads as two clauses",
			doing: "Couldn't save the secret",
			err: func(t *testing.T) error {
				dir := t.TempDir()
				link := &os.LinkError{Op: "link", Old: filepath.Join(dir, ".tmp-1"), New: filepath.Join(dir, "x.age"), Err: errors.New("operation not supported")}
				create := &fs.PathError{Op: "open", Path: filepath.Join(dir, "x.age"), Err: os.ErrPermission}
				// As the local backend words it (see writeFileExclusive).
				return fmt.Errorf("store.Create: write: writeFileExclusive: %w; creating the file directly also failed: %w", link, create)
			},
			want: "Couldn't save the secret: can't link ",
		},
		{
			name:  "an unreadable vlt.json is named",
			doing: "Couldn't open the vault",
			err: func(t *testing.T) error {
				_, dir := openTestVault(t)
				if err := os.WriteFile(filepath.Join(dir, "vlt.json"), []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := store.Open(ctx, dir, testPassphrase, quietLog(), testKDF)
				return err
			},
			want: "Couldn't open the vault: invalid character 'n' looking for beginning of object key string (in vlt.json)",
		},
		{
			name:  "a file held by another secret names it",
			doing: "Couldn't save the secret",
			err: func(t *testing.T) error {
				s, _ := openTestVault(t)
				if err := s.Create(ctx, &store.Secret{Version: 1, Type: store.TypePassword, Name: "GitHub Key", Group: "work", Value: "x"}); err != nil {
					t.Fatal(err)
				}
				return s.Update(ctx, &store.Secret{Version: 1, Type: store.TypePassword, Name: "github-key", Group: "work", Value: "y"})
			},
			want: `Couldn't save the secret: a secret with that name already exists (its file is held by "GitHub Key")`,
		},
		{
			name:  "joined errors are each described",
			doing: "",
			err: func(*testing.T) error {
				return errors.Join(fmt.Errorf("store.Close: %w", os.ErrClosed), fmt.Errorf("store.Unlock: %w", store.ErrSecretNotFound))
			},
			want: "File already closed\nsecret not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.err(t)
			if err == nil {
				t.Fatal("the case made no error")
			}

			got := errorMessage(tt.doing, err)

			if codePrefix.MatchString(got) {
				t.Errorf("errorMessage shows a code prefix: %q (from %q)", got, err)
			}
			if !strings.HasPrefix(got, tt.want) {
				t.Errorf("errorMessage = %q, want it to start %q (from %q)", got, tt.want, err)
			}
		})
	}
}

func TestErrorMessage_UnwritableFolderSaysWhichAndWhy(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not restrict this user")
	}
	s, _ := openTestVault(t)
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	dest := filepath.Join(parent, "moved")

	got := errorMessage("Couldn't move the vault", s.CopyTo(context.Background(), dest))

	if want := "Couldn't move the vault: can't create the folder " + dest + ": permission denied"; got != want {
		t.Fatalf("errorMessage = %q, want %q", got, want)
	}
}

func TestErrorMessage_LeadsWithDoingOrStandsAlone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		doing string
		err   error
		want  string
	}{
		{"no error gives just doing", "Couldn't save", nil, "Couldn't save"},
		{"no doing gives a capitalised cause", "", errors.New("passphrases do not match"), "Passphrases do not match"},
		{"a prefix is dropped", "", fmt.Errorf("store.Get %q: %w", "x", store.ErrSecretNotFound), "Secret not found"},
		{"a message without its cause's text is kept", "", &rewordedError{causes: []error{errors.New("raw")}, text: "reworded"}, "Reworded"},
		{
			"a cause the message doesn't repeat is skipped", "",
			&rewordedError{causes: []error{fmt.Errorf("store.A: %w", errors.New("base")), errors.New("unsaid")}, text: "store.A: base; reworded"},
			"Base; reworded",
		},
		{"a prefix repeating its cause's text is dropped", "", fmt.Errorf("a: %w", errors.New("a")), "A"},
		{"a cause wrapped twice in a file error keeps its words", "", &fs.PathError{Op: "write", Path: "x", Err: fmt.Errorf("store.A: %w (detail)", os.ErrNotExist)}, "Can't write x: file does not exist (detail)"},
		{"a missing file is said plainly", "", missingFileError(t), "Can't open " + filepath.Join(os.TempDir(), "vlt-no-such-file") + ": it does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := errorMessage(tt.doing, tt.err); got != tt.want {
				t.Fatalf("errorMessage = %q, want %q", got, tt.want)
			}
		})
	}
}

// rewordedError wraps causes under a message of its own, which may leave
// some of them out.
type rewordedError struct {
	causes []error
	text   string
}

func (e *rewordedError) Error() string   { return e.text }
func (e *rewordedError) Unwrap() []error { return e.causes }

// missingFileError is the error os gives for opening a file that isn't there.
func missingFileError(t *testing.T) error {
	t.Helper()
	_, err := os.ReadFile(filepath.Join(os.TempDir(), "vlt-no-such-file"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadFile = %v, want a missing file", err)
	}
	return err
}
