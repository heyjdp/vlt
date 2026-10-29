package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
)

// pipeServer serves SFTP in-process over net.Pipe, from the real file
// system (pkg/sftp's server), so the remote backend is tested without ssh
// or a network. It counts dials and can drop every open session.
type pipeServer struct {
	dials   atomic.Int32
	mu      sync.Mutex
	servers []*sftp.Server
}

func (p *pipeServer) dial(_ context.Context, _ Location) (*sftp.Client, io.Closer, error) {
	p.dials.Add(1)
	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn)
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	p.servers = append(p.servers, server)
	p.mu.Unlock()
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		_ = server.Close()
		return nil, nil, err
	}
	return client, closerFunc(func() error {
		err := client.Close()
		_ = server.Close()
		return err
	}), nil
}

// drop ends every session from the server side, as a network failure would.
func (p *pipeServer) drop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.servers {
		_ = s.Close()
	}
	p.servers = nil
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// remoteLocation is a "server" location whose files live in a temp dir.
func remoteLocation(t *testing.T) (Location, string) {
	t.Helper()
	dir := filepath.ToSlash(t.TempDir())
	return Location{Host: "test-server", Path: dir}, dir
}

func newTestRemote(t *testing.T) (*remoteFS, *pipeServer) {
	t.Helper()
	loc, _ := remoteLocation(t)
	srv := &pipeServer{}
	r, err := newRemoteFS(context.Background(), loc, srv.dial)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, srv
}

// backends returns each vaultFS implementation over a fresh directory. The
// remote one is left out on Windows, where pkg/sftp's server needs slash
// paths.
func backends(t *testing.T) map[string]vaultFS {
	t.Helper()
	b := map[string]vaultFS{"local": localFS{root: t.TempDir()}}
	if runtime.GOOS != "windows" {
		b["remote"], _ = newTestRemote(t)
	}
	return b
}

func TestVaultFS_Contract(t *testing.T) {
	for name, fsys := range backends(t) {
		t.Run(name, func(t *testing.T) {
			if err := fsys.MkdirAll("work/deep"); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := fsys.MkdirAll("work/deep"); err != nil {
				t.Fatalf("MkdirAll of an existing directory: %v", err)
			}
			info, err := fsys.Stat("work")
			if err != nil || !info.IsDir() {
				t.Fatalf("Stat(work) = %v, %v", info, err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != dirPerm {
				t.Fatalf("directory mode %v, want %v", info.Mode().Perm(), dirPerm)
			}

			if err := fsys.WriteFileExclusive("work/a.age", []byte("one")); err != nil {
				t.Fatalf("WriteFileExclusive: %v", err)
			}
			if err := fsys.WriteFileExclusive("work/a.age", []byte("two")); !errors.Is(err, fs.ErrExist) {
				t.Fatalf("second WriteFileExclusive = %v, want ErrExist", err)
			}
			if got, _ := fsys.ReadFile("work/a.age"); string(got) != "one" {
				t.Fatalf("exclusive write replaced the file: %q", got)
			}

			if err := fsys.WriteFileAtomic("work/a.age", []byte("three")); err != nil {
				t.Fatalf("WriteFileAtomic over a file: %v", err)
			}
			if err := fsys.WriteFileAtomic("work/b.age", []byte("new")); err != nil {
				t.Fatalf("WriteFileAtomic of a new file: %v", err)
			}
			if got, _ := fsys.ReadFile("work/a.age"); string(got) != "three" {
				t.Fatalf("atomic write: got %q", got)
			}
			info, err = fsys.Stat("work/b.age")
			if err != nil {
				t.Fatal(err)
			}
			if runtime.GOOS != "windows" && info.Mode().Perm() != filePerm {
				t.Fatalf("file mode %v, want %v", info.Mode().Perm(), filePerm)
			}

			entries, err := fsys.ReadDir("work")
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			// Sorted by name, as os.ReadDir does: the secret list keeps
			// directory order.
			if !slices.Equal(names, []string{"a.age", "b.age", "deep"}) {
				t.Fatalf("ReadDir = %v (unsorted, or temp files left behind?)", names)
			}
			if err := fsys.WriteFileAtomic("c.age", []byte("c")); err != nil {
				t.Fatal(err)
			}
			if err := fsys.Rename("c.age", "work/a.age"); err != nil {
				t.Fatalf("Rename over a file: %v", err)
			}
			if got, _ := fsys.ReadFile("work/a.age"); string(got) != "c" {
				t.Fatalf("Rename: got %q", got)
			}

			if err := fsys.Remove("work/b.age"); err != nil {
				t.Fatal(err)
			}
			for _, op := range []func() error{
				func() error { _, err := fsys.ReadFile("work/b.age"); return err },
				func() error { _, err := fsys.Stat("work/b.age"); return err },
				func() error { _, err := fsys.ReadDir("missing"); return err },
				func() error { return fsys.Remove("work/b.age") },
			} {
				if err := op(); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("operation on a missing file = %v, want ErrNotExist", err)
				}
			}
			if err := fsys.SyncDir("work"); err != nil {
				t.Fatalf("SyncDir: %v", err)
			}
		})
	}
}

func TestRemoteStore_FullLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pkg/sftp's server needs slash paths")
	}
	loc, dir := remoteLocation(t)
	srv := &pipeServer{}
	opts := []Option{WithWorkFactor(10), withDial(srv.dial)}
	ctx := context.Background()

	if ok, err := Exists(ctx, loc.String(), opts...); err != nil || ok {
		t.Fatalf("Exists before create = %v, %v", ok, err)
	}
	s, err := Create(ctx, loc.String(), internalPass, quiet(), opts...)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if s.Dir() != loc.String() || !s.Location().IsRemote() {
		t.Fatalf("Dir() = %q", s.Dir())
	}
	sec := &Secret{Type: TypePassword, Name: "GitHub", Group: "Work", Value: "ghp_secret"}
	if err := s.Create(ctx, sec); err != nil {
		t.Fatalf("Create secret: %v", err)
	}
	if err := s.Create(ctx, &Secret{Type: TypePassword, Name: "github", Group: "work"}); !errors.Is(err, ErrSecretExists) {
		t.Fatalf("slug collision = %v, want ErrSecretExists", err)
	}
	sec.Value = "ghp_rotated"
	if err := s.Update(ctx, sec); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := s.ChangePassphrase(ctx, internalPass, "a new passphrase here"); err != nil {
		t.Fatalf("ChangePassphrase: %v", err)
	}
	s.Close()
	<-s.Closed()

	// Only ciphertext reached the server.
	raw, err := os.ReadFile(filepath.Join(dir, "work", "github.age"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ghp_rotated") {
		t.Fatal("the secret's value is on the server in plain text")
	}

	s, err = Open(ctx, loc.String(), "a new passphrase here", quiet(), opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { s.Close(); <-s.Closed() }()
	got, err := s.List(ctx)
	if err != nil || len(got) != 1 || got[0].Value != "ghp_rotated" {
		t.Fatalf("List = %v, %v", got, err)
	}

	// Copy the remote vault to a local folder and back to another remote one.
	local := t.TempDir()
	if err := s.CopyTo(ctx, local); err != nil {
		t.Fatalf("CopyTo local: %v", err)
	}
	other, _ := remoteLocation(t)
	if err := s.CopyTo(ctx, other.String()); err != nil {
		t.Fatalf("CopyTo remote: %v", err)
	}
	if err := s.CopyTo(ctx, other.String()); !errors.Is(err, ErrVaultExists) {
		t.Fatalf("second CopyTo = %v, want ErrVaultExists", err)
	}
	for _, target := range []string{local, other.String()} {
		copied, err := Open(ctx, target, "a new passphrase here", quiet(), opts...)
		if err != nil {
			t.Fatalf("Open copy at %s: %v", target, err)
		}
		if g, err := copied.Get(ctx, "GitHub", "Work"); err != nil || g.Value != "ghp_rotated" {
			t.Fatalf("copy at %s: %v, %v", target, g, err)
		}
		copied.Close()
		<-copied.Closed()
	}

	if err := s.Delete(ctx, "GitHub", "Work"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "GitHub", "Work"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("Get after delete = %v", err)
	}
}

func TestRemoteFS_ReconnectsAfterTheConnectionDrops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pkg/sftp's server needs slash paths")
	}
	r, srv := newTestRemote(t)
	if err := r.WriteFileAtomic("a", []byte("x")); err != nil {
		t.Fatal(err)
	}

	srv.drop()
	_, err := r.ReadFile("a")
	if !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("read on a dropped connection = %v, want ErrConnectionLost", err)
	}
	got, err := r.ReadFile("a")
	if err != nil || string(got) != "x" {
		t.Fatalf("read after reconnecting = %q, %v", got, err)
	}
	if n := srv.dials.Load(); n != 2 {
		t.Fatalf("dialled %d times, want 2 (one reconnect)", n)
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFile("a"); !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("read after Close = %v", err)
	}
	if n := srv.dials.Load(); n != 2 {
		t.Fatal("a closed remote must not reconnect")
	}
}

// Not parallel: pkg/sftp's advertised extensions are a package variable.
func TestRemoteFS_RefusesAServerWithoutSafeWrites(t *testing.T) {
	if err := sftp.SetSFTPExtensions("posix-rename@openssh.com"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = sftp.SetSFTPExtensions("hardlink@openssh.com", "posix-rename@openssh.com", "statvfs@openssh.com")
	}()
	loc, _ := remoteLocation(t)
	srv := &pipeServer{}
	_, err := newRemoteFS(context.Background(), loc, srv.dial)
	if !errors.Is(err, ErrServerUnsupported) || !strings.Contains(err.Error(), extHardlink) {
		t.Fatalf("newRemoteFS = %v, want ErrServerUnsupported naming %s", err, extHardlink)
	}
}

func TestSSHArgs(t *testing.T) {
	t.Parallel()
	got := sshArgs(Location{Host: "vault.example", User: "me", Port: 2222, Path: "/srv/vault"})
	want := []string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=15",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
		"-T", "-x", "-a",
		"-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "RemoteCommand=none",
		"-p", "2222", "-l", "me", "-s", "--", "vault.example", "sftp",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sshArgs =\n%q\nwant\n%q", got, want)
	}
	// Without a user or port, ~/.ssh/config decides them.
	got = sshArgs(Location{Host: "nas", Path: "vault"})
	if slices.Contains(got, "-l") || slices.Contains(got, "-p") {
		t.Fatalf("sshArgs without user or port = %q", got)
	}
}

// A fake ssh that fails like a refused key shows the user what ssh said.
// Not parallel: it changes PATH.
func TestDialSSH_ReportsWhatSSHPrinted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ssh")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'me@vault.example: Permission denied (publickey).' >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	_, err := Exists(context.Background(), "me@vault.example:/srv/vault")
	if !errors.Is(err, ErrUnreachable) || !strings.Contains(err.Error(), "Permission denied (publickey)") {
		t.Fatalf("Exists = %v, want ErrUnreachable with ssh's message", err)
	}
}

// TestRemoteStore_RealServer runs against a real SSH server when
// VLT_TEST_SSH_LOCATION names a folder there (it must not exist yet, or be
// empty), e.g. VLT_TEST_SSH_LOCATION=me@nas:/tmp/vlt-test. It leaves its
// test vault there; delete the folder before running it again.
func TestRemoteStore_RealServer(t *testing.T) {
	location := os.Getenv("VLT_TEST_SSH_LOCATION")
	if location == "" {
		t.Skip("VLT_TEST_SSH_LOCATION is not set")
	}
	ctx := context.Background()
	opts := []Option{WithWorkFactor(10)}
	if ok, err := Exists(ctx, location, opts...); err != nil || ok {
		t.Fatalf("Exists = %v, %v; the test folder must be empty or absent", ok, err)
	}
	s, err := Create(ctx, location, internalPass, quiet(), opts...)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r := s.fs.(*remoteFS)
	if c, err := r.session(); err != nil || !c.fsync {
		t.Errorf("the server should offer fsync@openssh.com (%v)", err)
	}
	if err := s.AcquireLock(ctx, false); err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	for i := range 20 {
		sec := &Secret{Type: TypePassword, Name: "secret " + string(rune('a'+i)), Group: "test", Value: "v"}
		if err := s.Create(ctx, sec); err != nil {
			t.Fatalf("Create secret: %v", err)
		}
	}
	got, err := s.List(ctx)
	if err != nil || len(got) != 20 {
		t.Fatalf("List = %d secrets, %v", len(got), err)
	}
	s.Close()
	<-s.Closed()
	s, err = Open(ctx, location, internalPass, quiet(), opts...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { s.Close(); <-s.Closed() }()
	if _, err := s.fs.Stat(lockFile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the lock should be released on Close: %v", err)
	}
	for name, want := range map[string]fs.FileMode{".": dirPerm, tmpDir: dirPerm, "test": dirPerm, keyFile: filePerm, "test/secret-a.age": filePerm} {
		info, err := s.fs.Stat(name)
		if err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: %v, %v; want mode %v", name, info, err, want)
		}
	}
}

// openRemoteVault creates a vault on the pipe server and returns it open.
func openRemoteVault(t *testing.T) (*Store, *pipeServer, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("pkg/sftp's server needs slash paths")
	}
	loc, dir := remoteLocation(t)
	srv := &pipeServer{}
	s, err := Create(context.Background(), loc.String(), internalPass, quiet(), WithWorkFactor(10), withDial(srv.dial))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); <-s.Closed() })
	return s, srv, dir
}

func TestRemoteStore_ListKeepsNameOrderWithManyFiles(t *testing.T) {
	s, _, _ := openRemoteVault(t)
	ctx := context.Background()
	var want []string
	for i := 29; i >= 0; i-- { // created in reverse, so creation order differs
		name := fmt.Sprintf("s%02d", i)
		if err := s.Create(ctx, &Secret{Type: TypePassword, Name: name, Group: "g", Value: "v"}); err != nil {
			t.Fatal(err)
		}
		want = append([]string{name}, want...)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, sec := range got {
		names = append(names, sec.Name)
	}
	if !slices.Equal(names, want) {
		t.Fatalf("List order = %v", names)
	}
}

func TestRemoteStore_CloseReleasesTheLock(t *testing.T) {
	s, _, dir := openRemoteVault(t)
	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, lockFile)); err != nil {
		t.Fatalf("lock not written: %v", err)
	}
	s.Close()
	<-s.Closed()
	if _, err := os.Stat(filepath.Join(dir, lockFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a remote Close must release the lock: %v", err)
	}
}

func TestRemoteStore_TempFilesStayInAPrivateFolderThatIsNotAGroup(t *testing.T) {
	s, _, dir := openRemoteVault(t)
	ctx := context.Background()
	if err := s.Create(ctx, &Secret{Type: TypePassword, Name: "a", Group: "g", Value: "v"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, tmpDir))
	if err != nil || info.Mode().Perm() != dirPerm {
		t.Fatalf("temp folder: %v, %v; want mode %v", info, err, dirPerm)
	}
	if leftovers, _ := os.ReadDir(filepath.Join(dir, tmpDir)); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	// A stray .age file in the temp folder is never taken for a secret.
	if err := os.WriteFile(filepath.Join(dir, tmpDir, "x.age"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %d, %v", len(got), err)
	}
	dest := t.TempDir()
	if err := s.CopyTo(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, tmpDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("CopyTo must not copy the temp folder")
	}
}

func TestRemoteFS_LoosenedTempFolderIsMadePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pkg/sftp's server needs slash paths")
	}
	r, _ := newTestRemote(t)
	dir := filepath.FromSlash(r.loc.Path)
	if err := os.Mkdir(filepath.Join(dir, tmpDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, tmpDir), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFileAtomic("a", []byte("x")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, tmpDir))
	if err != nil || info.Mode().Perm() != dirPerm {
		t.Fatalf("temp folder: %v, %v; want mode %v", info, err, dirPerm)
	}
}

func TestRemoteStore_ListFailsWhenTheConnectionDrops(t *testing.T) {
	s, srv, _ := openRemoteVault(t)
	ctx := context.Background()
	if err := s.Create(ctx, &Secret{Type: TypePassword, Name: "a", Group: "g", Value: "v"}); err != nil {
		t.Fatal(err)
	}
	// Drop the connection after the directory reads, before the file reads.
	inner := s.fs
	s.fs = &dropOnRead{vaultFS: inner, srv: srv}
	defer func() { s.fs = inner }()
	if _, err := s.List(ctx); !errors.Is(err, ErrConnectionLost) {
		t.Fatalf("List = %v, want ErrConnectionLost rather than a partial list", err)
	}
}

// dropOnRead drops the pipe server's sessions before the first file read.
type dropOnRead struct {
	vaultFS
	srv  *pipeServer
	once sync.Once
}

func (d *dropOnRead) ReadFile(name string) ([]byte, error) {
	d.once.Do(d.srv.drop)
	return d.vaultFS.ReadFile(name)
}

func TestPlainEntryName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"work": true, "a.age": true, ".vlt-tmp": true,
		"": false, ".": false, "..": false, "x/..": false, `a\b`: false, "../evil": false,
	} {
		if got := plainEntryName(name); got != want {
			t.Errorf("plainEntryName(%q) = %v", name, got)
		}
	}
}
