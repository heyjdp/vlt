package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

// A vault on an SSH server is reached through the system's ssh client: vlt
// runs `ssh -s host sftp` and speaks SFTP over its stdin and stdout. ssh
// therefore applies the user's ~/.ssh/config (aliases, users, ports,
// ProxyJump), keys, ssh-agent and known_hosts. vlt cannot answer prompts, so
// ssh runs in batch mode: the key must be usable without a passphrase prompt
// (an agent, or an unencrypted key) and the host must already be in
// known_hosts.
//
// Secrets and the wrapped vault key are age-encrypted on this computer
// before they are written; only vlt.json (the format number) and vlt.lock
// (who has the vault open) are plain text.

// Errors for vaults on a server.
var (
	// ErrUnreachable is returned when ssh cannot connect or log in.
	ErrUnreachable = errors.New("cannot connect to the vault's server")
	// ErrConnectionLost is returned when an established connection drops.
	// The next operation reconnects.
	ErrConnectionLost = errors.New("lost the connection to the vault's server")
	// ErrServerUnsupported is returned when the SFTP server lacks an
	// extension vlt needs for safe writes.
	ErrServerUnsupported = errors.New("the server's SFTP does not support safe writes")
)

// Extensions vlt needs: posix-rename replaces a file atomically (the plain
// SFTP rename refuses to overwrite), and hardlink creates a file without
// ever overwriting one. OpenSSH's sftp-server has both, and fsync, which is
// used when offered.
const (
	extPosixRename = "posix-rename@openssh.com"
	extHardlink    = "hardlink@openssh.com"
	extFsync       = "fsync@openssh.com"
)

// sshConnectTimeout bounds connecting and logging in. ServerAlive settings
// notice a dead connection after about 45 seconds.
const sshConnectTimeout = 15 * time.Second

// dialFunc opens an SFTP session to loc. The closer ends the session.
type dialFunc func(ctx context.Context, loc Location) (*sftp.Client, io.Closer, error)

// sshArgs returns the ssh arguments for an SFTP session to loc. The host and
// user were validated by ParseLocation, and the host follows "--", so
// neither can be read as an option.
func sshArgs(loc Location) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(int(sshConnectTimeout/time.Second)),
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		// No terminal, no X11 or agent forwarding, and none of the
		// forwardings or commands ~/.ssh/config may set for this host: the
		// session only carries SFTP.
		"-T", "-x", "-a",
		"-o", "ClearAllForwardings=yes",
		"-o", "PermitLocalCommand=no",
		"-o", "RemoteCommand=none",
	}
	if loc.Port != 0 {
		args = append(args, "-p", strconv.Itoa(loc.Port))
	}
	if loc.User != "" {
		args = append(args, "-l", loc.User)
	}
	return append(args, "-s", "--", loc.Host, "sftp")
}

// sshSession is a running ssh process carrying an SFTP session.
type sshSession struct {
	cmd    *exec.Cmd
	client *sftp.Client
	stderr *tailBuffer
}

// Close ends the SFTP session and waits for ssh to exit, killing it if it
// does not exit promptly.
func (s *sshSession) Close() error {
	err := s.client.Close()
	exited := make(chan struct{})
	go func() {
		_ = s.cmd.Wait() // ssh exits non-zero when the session is cut; not an error here
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill()
		<-exited
	}
	return err //nolint:wrapcheck // closing; nothing more to add
}

// dialSSH starts ssh and opens an SFTP session over it. If ssh fails to
// connect or log in, the error carries what ssh printed.
func dialSSH(ctx context.Context, loc Location) (*sftp.Client, io.Closer, error) {
	bin, err := exec.LookPath("ssh")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: no ssh client found: %w", ErrUnreachable, err)
	}
	cmd := exec.Command(bin, sshArgs(loc)...) //nolint:gosec // fixed binary; arguments validated by ParseLocation
	// A ProxyJump or ProxyCommand child can keep ssh's pipes open after ssh
	// exits; don't let it hold up Wait.
	cmd.WaitDelay = 2 * time.Second
	hideConsole(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	stderr := &tailBuffer{max: 2048}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("%w: start ssh: %w", ErrUnreachable, err)
	}

	type result struct {
		client *sftp.Client
		err    error
	}
	ready := make(chan result, 1)
	go func() {
		c, err := sftp.NewClientPipe(stdout, stdin)
		ready <- result{c, err}
	}()
	var r result
	select {
	case r = <-ready:
	case <-ctx.Done():
		r.err = ctx.Err()
	}
	if r.err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if r.client != nil {
			_ = r.client.Close()
		}
		if msg := stderr.String(); msg != "" {
			return nil, nil, fmt.Errorf("%w %s: %s", ErrUnreachable, loc.Server(), msg)
		}
		return nil, nil, fmt.Errorf("%w %s: %w", ErrUnreachable, loc.Server(), r.err)
	}
	return r.client, &sshSession{cmd: cmd, client: r.client, stderr: stderr}, nil
}

// tailBuffer keeps the last max bytes written to it (ssh's error output).
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.max; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

// String returns the buffered output, trimmed, on one line.
func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Fields(strings.ReplaceAll(string(bytes.TrimSpace(b.buf)), "\r", ""))
	return strings.Join(lines, " ")
}

// remoteFS is a vault directory on an SSH server. It connects when created
// and, after a dropped connection, reconnects on the next operation; the
// operation that saw the drop fails with ErrConnectionLost rather than being
// retried, because a write may or may not have landed.
type remoteFS struct {
	loc  Location
	dial dialFunc

	mu     sync.Mutex
	conn   *sftpConn // nil after a drop, until the next operation
	closed bool
}

// sftpConn is one SFTP session and what the server offers on it.
type sftpConn struct {
	client *sftp.Client
	closer io.Closer
	fsync  bool // the server offers fsync@openssh.com

	tmpOnce sync.Once
	tmpErr  error // from preparing tmpDir, once per session
}

// tmpDir holds in-flight writes. It is private (0700), so a temp file can
// never be opened by another user in the moment before its own mode is set
// (SFTP creates files with the server's umask). Its name starts with a dot,
// which no group directory can (group names are slugs), so directory walks
// skip it.
const tmpDir = ".vlt-tmp"

// staleTempAge is how old a leftover temp file must be (from a dropped
// connection) before it is removed on the next connection.
const staleTempAge = time.Hour

func newRemoteFS(ctx context.Context, loc Location, dial dialFunc) (*remoteFS, error) {
	r := &remoteFS{loc: loc, dial: dial}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.connectLocked(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

// connectLocked dials the server and checks it supports safe writes.
func (r *remoteFS) connectLocked(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, sshConnectTimeout+5*time.Second)
	defer cancel()
	client, closer, err := r.dial(ctx, r.loc)
	if err != nil {
		return err
	}
	for _, ext := range []string{extPosixRename, extHardlink} {
		if _, ok := client.HasExtension(ext); !ok {
			_ = closer.Close()
			return fmt.Errorf("%w: %s lacks %s (OpenSSH's sftp-server has it)", ErrServerUnsupported, r.loc.Server(), ext)
		}
	}
	_, fsync := client.HasExtension(extFsync)
	r.conn = &sftpConn{client: client, closer: closer, fsync: fsync}
	return nil
}

// session returns the connected session, reconnecting after a drop.
func (r *remoteFS) session() (*sftpConn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("%w: closed", ErrConnectionLost)
	}
	if r.conn == nil {
		if err := r.connectLocked(context.Background()); err != nil {
			return nil, err
		}
	}
	return r.conn, nil
}

// do runs op on the session. If the connection dropped, the session is
// discarded so the next operation reconnects.
func (r *remoteFS) do(op func(c *sftpConn) error) error {
	c, err := r.session()
	if err != nil {
		return err
	}
	err = op(c)
	if err != nil && isConnectionError(err) {
		r.mu.Lock()
		if r.conn == c {
			_ = c.closer.Close()
			r.conn = nil
		}
		r.mu.Unlock()
		return fmt.Errorf("%w %s: %w", ErrConnectionLost, r.loc.Server(), err)
	}
	return err
}

// isConnectionError reports whether err means the SFTP session is gone.
// io.EOF counts: the operations used here only return it when the stream
// ends (io.ReadAll and ReadDir absorb a file's or listing's normal end).
func isConnectionError(err error) bool {
	return errors.Is(err, sftp.ErrSSHFxConnectionLost) || errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed)
}

// abs returns name's path on the server.
func (r *remoteFS) abs(name string) string { return path.Join(r.loc.Path, name) }

func (r *remoteFS) ReadFile(name string) ([]byte, error) {
	var data []byte
	err := r.do(func(c *sftpConn) error {
		f, err := c.client.Open(r.abs(name))
		if err != nil {
			return err
		}
		data, err = io.ReadAll(f)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		return err
	})
	if err != nil {
		return nil, errorf("read", name, err)
	}
	return data, nil
}

// ReadDir lists a directory sorted by name, as os.ReadDir does. Names the
// server returns that are not a plain file name ("", ".", "..", or with a
// slash or backslash) are dropped: a hostile server must not be able to
// steer a copy out of the vault folder.
func (r *remoteFS) ReadDir(name string) ([]fs.DirEntry, error) {
	var entries []fs.DirEntry
	err := r.do(func(c *sftpConn) error {
		infos, err := c.client.ReadDir(r.abs(name))
		if err != nil {
			return err
		}
		for _, info := range infos {
			if !plainEntryName(info.Name()) {
				continue
			}
			entries = append(entries, fs.FileInfoToDirEntry(info))
		}
		return nil
	})
	if err != nil {
		return nil, errorf("readdir", name, err)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// plainEntryName reports whether a name from a directory listing is a
// single path element.
func plainEntryName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, `/\`)
}

func (r *remoteFS) Stat(name string) (fs.FileInfo, error) {
	var info fs.FileInfo
	err := r.do(func(c *sftpConn) error {
		var err error
		info, err = c.client.Stat(r.abs(name))
		return err
	})
	if err != nil {
		return nil, errorf("stat", name, err)
	}
	return info, nil
}

func (r *remoteFS) Remove(name string) error {
	if err := r.do(func(c *sftpConn) error { return c.client.Remove(r.abs(name)) }); err != nil {
		return errorf("remove", name, err)
	}
	return nil
}

// Rename replaces newname with oldname atomically (posix-rename).
func (r *remoteFS) Rename(oldname, newname string) error {
	if err := r.do(func(c *sftpConn) error { return c.client.PosixRename(r.abs(oldname), r.abs(newname)) }); err != nil {
		return errorf("rename", oldname, err)
	}
	return nil
}

// MkdirAll creates each missing directory with mode 0700. SFTP's mkdir
// applies the server's umask, so the mode is set explicitly afterwards.
func (r *remoteFS) MkdirAll(name string) error {
	if err := r.do(func(c *sftpConn) error { return mkdirAll(c.client, r.abs(name)) }); err != nil {
		return errorf("mkdir", name, err)
	}
	return nil
}

func mkdirAll(c *sftp.Client, target string) error {
	if info, err := c.Stat(target); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", target)
		}
		return nil
	}
	var missing []string
	for dir := target; dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if _, err := c.Stat(dir); err == nil {
			break
		}
		missing = append(missing, dir)
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := c.Mkdir(missing[i]); err != nil {
			// Another writer may have created it meanwhile.
			if info, statErr := c.Stat(missing[i]); statErr != nil || !info.IsDir() {
				return err
			}
			continue
		}
		if err := c.Chmod(missing[i], dirPerm); err != nil {
			return err
		}
	}
	return nil
}

// prepareTmp makes sure the private temp folder exists with mode 0700, and
// removes temp files a dropped connection left behind. It runs once per
// session.
func (r *remoteFS) prepareTmp(c *sftpConn) error {
	c.tmpOnce.Do(func() {
		dir := r.abs(tmpDir)
		if err := mkdirAll(c.client, dir); err != nil {
			c.tmpErr = err
			return
		}
		info, err := c.client.Stat(dir)
		if err != nil {
			c.tmpErr = err
			return
		}
		if info.Mode().Perm() != dirPerm {
			// Created by someone else, or loosened since: tighten it, or
			// refuse to write through it.
			if err := c.client.Chmod(dir, dirPerm); err != nil {
				c.tmpErr = fmt.Errorf("%s has mode %v and cannot be made private: %w", dir, info.Mode().Perm(), err)
				return
			}
		}
		stale, err := c.client.ReadDir(dir)
		if err != nil {
			return
		}
		for _, f := range stale {
			if strings.HasPrefix(f.Name(), tmpPrefix) && !f.IsDir() && time.Since(f.ModTime()) > staleTempAge {
				_ = c.client.Remove(path.Join(dir, f.Name()))
			}
		}
	})
	return c.tmpErr
}

// writeTemp writes data to a new, uniquely named file in the private temp
// folder, mode 0600, fsynced when the server offers it. It returns the temp
// file's path.
func (r *remoteFS) writeTemp(c *sftpConn, data []byte) (string, error) {
	if err := r.prepareTmp(c); err != nil {
		return "", err
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	tmp := path.Join(r.abs(tmpDir), tmpPrefix+hex.EncodeToString(suffix))
	if err := writeNew(c, tmp, data); err != nil {
		return "", err
	}
	return tmp, nil
}

// writeNew creates target exclusively, sets mode 0600 before writing, writes
// data and fsyncs it when the server offers that. On failure the file is
// removed.
func writeNew(c *sftpConn, target string, data []byte) error {
	f, err := c.client.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	err = f.Chmod(filePerm)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil && c.fsync {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = c.client.Remove(target)
		return err
	}
	return nil
}

// WriteFileAtomic writes a temp file and renames it over name with
// posix-rename, which replaces the target atomically.
func (r *remoteFS) WriteFileAtomic(name string, data []byte) error {
	err := r.do(func(c *sftpConn) error {
		tmp, err := r.writeTemp(c, data)
		if err != nil {
			return err
		}
		if err := c.client.PosixRename(tmp, r.abs(name)); err != nil {
			_ = c.client.Remove(tmp)
			return err
		}
		return nil
	})
	if err != nil {
		return errorf("write", name, err)
	}
	return nil
}

// WriteFileExclusive writes a temp file and hard-links it into place; the
// link fails if name exists, so no file is ever replaced. SFTP reports that
// failure without a reason, so the target is checked to tell "exists" apart.
// Where the server's file system cannot hard-link (exFAT, some network
// mounts) it falls back to creating name exclusively, which keeps the
// no-overwrite guarantee but not crash atomicity, as the local backend does.
func (r *remoteFS) WriteFileExclusive(name string, data []byte) error {
	err := r.do(func(c *sftpConn) error {
		target := r.abs(name)
		tmp, err := r.writeTemp(c, data)
		if err != nil {
			return err
		}
		defer func() { _ = c.client.Remove(tmp) }()
		linkErr := c.client.Link(tmp, target)
		if linkErr == nil {
			return nil
		}
		if isConnectionError(linkErr) {
			return linkErr
		}
		if _, statErr := c.client.Stat(target); statErr == nil {
			return fs.ErrExist
		}
		if err := writeNew(c, target, data); err != nil {
			if _, statErr := c.client.Stat(target); statErr == nil {
				return fs.ErrExist
			}
			return fmt.Errorf("link: %w; exclusive create: %w", linkErr, err)
		}
		return nil
	})
	if err != nil {
		return errorf("create", name, err)
	}
	return nil
}

// SyncDir does nothing: SFTP cannot fsync a directory. A crash of the server
// can therefore undo the most recent rename (leaving the previous version of
// that file), but never leave a partial file.
func (r *remoteFS) SyncDir(string) error { return nil }

func (r *remoteFS) Local() bool { return false }

// Close ends the session. Later operations fail.
func (r *remoteFS) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.conn == nil {
		return nil
	}
	err := r.conn.closer.Close()
	r.conn = nil
	return err
}

// String names the location in messages.
func (r *remoteFS) String() string { return r.loc.String() }

var _ vaultFS = (*remoteFS)(nil)
