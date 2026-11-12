package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// openedVault creates a local vault and returns it open, holding its lock.
func openedVault(t *testing.T) *Store {
	t.Helper()
	s, err := Create(context.Background(), t.TempDir(), internalPass, quiet(), WithWorkFactor(10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// closedVault creates a local vault, closes it (releasing its lock) and
// returns its folder.
func closedVault(t *testing.T) string {
	t.Helper()
	s := openedVault(t)
	s.Close()
	<-s.Closed()
	return s.loc.Path
}

// openAt opens the vault in dir, as a later unlock would.
func openAt(t *testing.T, dir string, opts ...Option) *Store {
	t.Helper()
	s, err := Open(context.Background(), dir, internalPass, quiet(), append([]Option{WithWorkFactor(10)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// recordingLog returns a logger that keeps warnings and errors in buf.
func recordingLog() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})), buf
}

// syncBuffer is a bytes.Buffer safe for concurrent use.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// plantLock writes a lock as another copy of vlt would.
func plantLock(t *testing.T, s *Store, info *LockInfo) {
	t.Helper()
	plantLockAt(t, s.loc.Path, info)
}

// plantLockAt writes a lock in the vault in dir as another copy of vlt
// would.
func plantLockAt(t *testing.T, dir string, info *LockInfo) {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := (localFS{root: dir}).WriteFileAtomic(lockFile, data); err != nil {
		t.Fatal(err)
	}
}

// lockAt reads the lock in the vault in dir from disk, past any faults a
// test injects.
func lockAt(t *testing.T, dir string) (LockInfo, error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, lockFile))
	if err != nil {
		return LockInfo{}, err
	}
	var info LockInfo
	err = json.Unmarshal(data, &info)
	return info, err
}

func TestLock_OpenAndCreateRecordThisProcessAndCloseReleasesIt(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"Create", "Open"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			s := openedVault(t)
			if how == "Open" {
				s = openAt(t, closedVault(t))
			}
			if _, elsewhere := s.OpenElsewhere(); elsewhere {
				t.Fatal("a vault nobody else has open is not open elsewhere")
			}
			held, err := lockAt(t, s.loc.Path)
			if err != nil {
				t.Fatal(err)
			}
			host, _ := os.Hostname()
			if held.Host != host || held.PID != os.Getpid() || held.Token == "" || time.Since(held.Since) > time.Minute {
				t.Fatalf("lock = %+v", held)
			}
			s.Close()
			<-s.Closed()
			if _, err := s.fs.Stat(lockFile); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Close should remove the lock: %v", err)
			}
		})
	}
}

func TestLock_AnotherComputersLockIsReportedAndCanBeTakenOver(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	since := time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC)
	plantLockAt(t, dir, &LockInfo{Host: "laptop.local", User: "me", PID: 4242, Since: since, Token: "theirs"})

	s := openAt(t, dir)
	holder, elsewhere := s.OpenElsewhere()
	if !elsewhere || holder.Host != "laptop.local" || holder.User != "me" || holder.PID != 4242 || !holder.Since.Equal(since) {
		t.Fatalf("OpenElsewhere = %+v, %v; want the lock held on laptop.local", holder, elsewhere)
	}
	if held, err := lockAt(t, dir); err != nil || held.Token != "theirs" {
		t.Fatalf("opening must leave the other copy's lock: %+v, %v", held, err)
	}

	if err := s.TakeOver(context.Background()); err != nil {
		t.Fatalf("TakeOver: %v", err)
	}
	if _, elsewhere := s.OpenElsewhere(); elsewhere {
		t.Fatal("once taken over, the vault is no longer open elsewhere")
	}
	if held, _ := lockAt(t, dir); held.Host == "laptop.local" {
		t.Fatal("TakeOver should take the lock over")
	}
	s.Close()
	<-s.Closed()
	if _, err := os.Stat(filepath.Join(dir, lockFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Close should release the lock taken over: %v", err)
	}
}

func TestLock_ALiveProcessOnThisComputerIsOpenElsewhere(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	host, _ := os.Hostname()
	plantLockAt(t, dir, &LockInfo{Host: host, PID: os.Getppid(), Token: "parent"}) // the test runner: alive

	if holder, elsewhere := openAt(t, dir).OpenElsewhere(); !elsewhere || holder.Token != "parent" {
		t.Fatalf("OpenElsewhere = %+v, %v; want the parent's lock", holder, elsewhere)
	}
}

func TestLock_AStaleLockOnThisComputerIsTakenOver(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	host, _ := os.Hostname()
	plantLockAt(t, dir, &LockInfo{Host: host, PID: 1 << 30, Token: "crashed"}) // no such process

	if _, elsewhere := openAt(t, dir).OpenElsewhere(); elsewhere {
		t.Fatal("a crashed copy's lock should be taken over")
	}
	if held, _ := lockAt(t, dir); held.PID != os.Getpid() {
		t.Fatalf("lock = %+v", held)
	}
}

func TestLock_ADamagedLockIsOpenElsewhere(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	if err := os.WriteFile(filepath.Join(dir, lockFile), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if holder, elsewhere := openAt(t, dir).OpenElsewhere(); !elsewhere || holder != (LockInfo{}) {
		t.Fatalf("OpenElsewhere = %+v, %v; want open elsewhere, holder unknown", holder, elsewhere)
	}
}

// lockReadFails makes reading the lock file fail with err, as a dropped
// connection or a file system error would: every time, or only the first
// times (a connection that comes back).
type lockReadFails struct {
	vaultFS
	err   error
	times int // 0 = always
	reads *int
}

func (f lockReadFails) ReadFile(name string) ([]byte, error) {
	if name == lockFile {
		*f.reads++
		if f.times == 0 || *f.reads <= f.times {
			return nil, f.err
		}
	}
	return f.vaultFS.ReadFile(name)
}

// lockFaults wraps a store's storage so that reading the lock fails.
func lockFaults(readErr error, times int) Option {
	return withFS(func(f vaultFS) vaultFS {
		return lockReadFails{vaultFS: f, err: readErr, times: times, reads: new(int)}
	})
}

// A lock that cannot be read for a reason that says nothing about who holds
// it does not make the vault open elsewhere: like a lock that cannot be
// written, it is logged and the vault opens.
func TestLock_ALockThatCannotBeReadIsLoggedNotOpenElsewhere(t *testing.T) {
	t.Parallel()
	for name, readErr := range map[string]error{
		"a dropped connection": ErrConnectionLost,
		"an I/O error":         errors.New("input/output error"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := closedVault(t)
			plantLockAt(t, dir, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
			log, logged := recordingLog()

			s, err := Open(context.Background(), dir, internalPass, log, WithWorkFactor(10), lockFaults(readErr, 0))
			if err != nil {
				t.Fatalf("a lock that cannot be read must not keep the vault shut: %v", err)
			}
			t.Cleanup(s.Close)
			if _, elsewhere := s.OpenElsewhere(); elsewhere {
				t.Fatal("a read error is not \"open elsewhere\"")
			}
			if !strings.Contains(logged.String(), "cannot record that the vault is open") || !strings.Contains(logged.String(), readErr.Error()) {
				t.Fatalf("the read error should be logged, got %q", logged)
			}
		})
	}
}

// A dropped connection is retried (the next call reconnects), so a lock
// held elsewhere is still found.
func TestLock_AConnectionThatComesBackStillFindsTheLock(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	plantLockAt(t, dir, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})

	s := openAt(t, dir, lockFaults(ErrConnectionLost, 1))
	if holder, elsewhere := s.OpenElsewhere(); !elsewhere || holder.Host != "laptop.local" {
		t.Fatalf("OpenElsewhere = %+v, %v; want the lock held on laptop.local", holder, elsewhere)
	}
}

// A lock vlt may not read most likely belongs to another user's copy.
func TestLock_ALockVltMayNotReadIsOpenElsewhere(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	plantLockAt(t, dir, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})

	s := openAt(t, dir, lockFaults(fs.ErrPermission, 0))
	if holder, elsewhere := s.OpenElsewhere(); !elsewhere || holder != (LockInfo{}) {
		t.Fatalf("OpenElsewhere = %+v, %v; want open elsewhere, holder unknown", holder, elsewhere)
	}
}

// lockWriteFails makes writing the lock fail, as a read-only folder would.
type lockWriteFails struct {
	vaultFS
	err error
}

func (f lockWriteFails) WriteFileExclusive(name string, data []byte) error {
	if name == lockFile {
		return f.err
	}
	return f.vaultFS.WriteFileExclusive(name, data)
}

// The lock only warns: failing to write it is logged, and the vault opens.
func TestLock_ALockThatCannotBeWrittenIsLoggedAndTheVaultOpens(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("read-only file system")
	faults := withFS(func(f vaultFS) vaultFS { return lockWriteFails{vaultFS: f, err: writeErr} })
	for _, how := range []string{"Create", "Open"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			log, logged := recordingLog()
			var s *Store
			var err error
			if how == "Create" {
				s, err = Create(context.Background(), t.TempDir(), internalPass, log, WithWorkFactor(10), faults)
			} else {
				s, err = Open(context.Background(), closedVault(t), internalPass, log, WithWorkFactor(10), faults)
			}
			if err != nil {
				t.Fatalf("a lock that cannot be written must not keep the vault shut: %v", err)
			}
			t.Cleanup(s.Close)
			if _, elsewhere := s.OpenElsewhere(); elsewhere {
				t.Fatal("a write error is not \"open elsewhere\"")
			}
			if !strings.Contains(logged.String(), "cannot record that the vault is open") || !strings.Contains(logged.String(), writeErr.Error()) {
				t.Fatalf("the write error should be logged, got %q", logged)
			}
			if _, err := s.fs.Stat(lockFile); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("no lock should be written: %v", err)
			}
		})
	}
}

func TestLock_CloseLeavesALockSomeoneElseTookOver(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	plantLock(t, s, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	s.Close()
	<-s.Closed()
	held, err := s.readLock()
	if err != nil || held.Token != "theirs" {
		t.Fatalf("someone else's lock was removed: %+v, %v", held, err)
	}
}

func TestLock_CloseWithoutALockTouchesNothing(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	plantLockAt(t, dir, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	s := openAt(t, dir) // open elsewhere: the lock is not this Store's
	s.Close()
	<-s.Closed()
	if _, err := s.readLock(); err != nil {
		t.Fatalf("a Store that never locked removed the lock: %v", err)
	}
}

func TestLock_LockFileIsNotPartOfTheVault(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	dest := t.TempDir()
	if err := s.CopyTo(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest + "/" + lockFile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("CopyTo must not copy the lock")
	}
	if secrets, err := s.Contents(context.Background()); err != nil || len(secrets.Secrets) != 0 {
		t.Fatalf("Contents = %v, %v", secrets.Secrets, err)
	}
}

func TestLock_ThisProcessesOwnLeftoverLockIsTakenOver(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	host, _ := os.Hostname()
	plantLockAt(t, dir, &LockInfo{Host: host, PID: os.Getpid(), Token: "earlier-session"})
	if _, elsewhere := openAt(t, dir).OpenElsewhere(); elsewhere {
		t.Fatal("this process's own leftover lock should be taken over")
	}
	if held, _ := lockAt(t, dir); held.Token == "earlier-session" {
		t.Fatal("the lock should now be this session's")
	}
}

func TestLock_ReleaseLeavesNoTempFiles(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	s.Close()
	<-s.Closed()
	entries, err := s.fs.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == lockFile || strings.HasPrefix(e.Name(), tmpPrefix) {
			t.Fatalf("left behind after release: %s", e.Name())
		}
	}
}

// renameHook runs after once its store has renamed the lock aside, as a
// release does first.
type renameHook struct {
	vaultFS
	after func()
}

func (f renameHook) Rename(oldname, newname string) error {
	err := f.vaultFS.Rename(oldname, newname)
	if err == nil && oldname == lockFile && f.after != nil {
		f.after()
	}
	return err
}

// The UI releases a lock in the background, so a quick lock and unlock can
// open the vault again while the old session's release is still running.
// Whichever way the two interleave, the new session's lock survives.
func TestLock_AReleaseStillRunningLeavesTheNextSessionsLock(t *testing.T) {
	t.Parallel()
	for _, when := range []string{"after the old lock was renamed aside", "before the release started"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()
			old := openedVault(t)
			var next *Store
			unlock := func() {
				var err error
				next, err = Open(context.Background(), old.loc.Path, internalPass, quiet(), WithWorkFactor(10))
				if err != nil {
					t.Errorf("unlocking again: %v", err)
					return
				}
				t.Cleanup(next.Close)
				if _, elsewhere := next.OpenElsewhere(); elsewhere {
					t.Error("the old session's lock is this process's own: it should be taken over")
				}
			}
			if when == "before the release started" {
				unlock() // takes over the old session's lock: same process
			} else {
				old.fs = renameHook{vaultFS: old.fs, after: unlock}
			}

			old.Close()
			<-old.Closed()
			if next == nil {
				t.Fatal("the vault was not unlocked again")
			}

			held, err := next.readLock()
			if err != nil || held.Token != next.lockToken || held.Token == "" {
				t.Fatalf("the next session's lock is gone: %+v, %v", held, err)
			}
		})
	}
}

// releaseFaults makes a release's steps fail after the lock was renamed
// aside: reading the aside file, or putting another copy's lock back.
type releaseFaults struct {
	vaultFS
	readAside error
	putBack   error
}

func (f releaseFaults) ReadFile(name string) ([]byte, error) {
	if f.readAside != nil && strings.HasPrefix(name, tmpPrefix+"lock-") {
		return nil, f.readAside
	}
	return f.vaultFS.ReadFile(name)
}

func (f releaseFaults) WriteFileExclusive(name string, data []byte) error {
	if f.putBack != nil && name == lockFile {
		return f.putBack
	}
	return f.vaultFS.WriteFileExclusive(name, data)
}

// A late release (Close stuck on a slow folder) can find a newer lock: the
// next session's, or another computer's after "Open Anyway". If the release
// cannot tell whose lock it moved aside, or cannot put it back, that lock is
// restored, never deleted.
func TestLock_AReleaseThatFailsMidwayKeepsTheNewerLock(t *testing.T) {
	t.Parallel()
	ioErr := errors.New("input/output error")
	for name, faults := range map[string]releaseFaults{
		"the lock moved aside cannot be read": {readAside: ioErr},
		"the newer lock cannot be put back":   {putBack: ioErr},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := openedVault(t)
			plantLock(t, s, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
			disk := s.fs
			faults.vaultFS = disk
			s.fs = faults

			s.Close()
			<-s.Closed()

			data, err := disk.ReadFile(lockFile)
			var held LockInfo
			if err == nil {
				err = json.Unmarshal(data, &held)
			}
			if err != nil || held.Token != "theirs" {
				t.Fatalf("the newer lock was lost: %+v, %v", held, err)
			}
		})
	}
}

// If a third copy locks the vault while a late release holds a newer lock
// aside, the newer lock cannot go back; it is left aside, not deleted.
func TestLock_AReleaseRacingAThirdCopyLeavesTheNewerLockAside(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	aside := tmpPrefix + "lock-" + s.lockToken
	plantLock(t, s, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	disk := s.fs
	third, err := json.Marshal(LockInfo{Host: "desktop.local", PID: 9, Token: "third"})
	if err != nil {
		t.Fatal(err)
	}
	s.fs = renameHook{vaultFS: disk, after: func() {
		if err := disk.WriteFileExclusive(lockFile, third); err != nil {
			t.Error(err)
		}
	}}

	s.Close()
	<-s.Closed()

	for file, want := range map[string]string{lockFile: "third", aside: "theirs"} {
		data, err := disk.ReadFile(file)
		var held LockInfo
		if err == nil {
			err = json.Unmarshal(data, &held)
		}
		if err != nil || held.Token != want {
			t.Errorf("%s = %+v, %v; want the lock %q", file, held, err, want)
		}
	}
}

// takeOverHook holds a takeover's write of the lock until proceed is
// closed, after telling entered.
type takeOverHook struct {
	vaultFS
	entered chan<- struct{}
	proceed <-chan struct{}
}

func (f takeOverHook) WriteFileAtomic(name string, data []byte) error {
	if name == lockFile {
		f.entered <- struct{}{}
		<-f.proceed
	}
	return f.vaultFS.WriteFileAtomic(name, data)
}

// Quitting or locking can close a waiting store while Open Anyway takes its
// lock over. Close waits for the takeover, then releases the lock it took:
// a closed Store never leaves a lock behind.
func TestLock_CloseDuringATakeOverWaitsForItAndLeavesNoLock(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	plantLockAt(t, dir, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	entered, proceed := make(chan struct{}), make(chan struct{})
	s := openAt(t, dir, withFS(func(f vaultFS) vaultFS {
		return takeOverHook{vaultFS: f, entered: entered, proceed: proceed}
	}))

	tookOver := make(chan error, 1)
	go func() { tookOver <- s.TakeOver(context.Background()) }()
	<-entered // the takeover is writing the lock
	go s.Close()
	select {
	case <-s.Closed():
		t.Fatal("Close finished while the lock was being taken over")
	case <-time.After(50 * time.Millisecond):
	}
	close(proceed)

	if err := <-tookOver; err != nil {
		t.Fatalf("a takeover begun before Close should finish: %v", err)
	}
	<-s.Closed()
	if _, err := os.Stat(filepath.Join(dir, lockFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Close should release the lock the takeover took: %v", err)
	}
}

func TestLock_TakeOverAfterCloseFailsAndLeavesTheLockAlone(t *testing.T) {
	t.Parallel()
	dir := closedVault(t)
	plantLockAt(t, dir, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	s := openAt(t, dir)
	s.Close()
	<-s.Closed()

	if err := s.TakeOver(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("TakeOver after Close = %v, want ErrClosed", err)
	}
	if held, err := lockAt(t, dir); err != nil || held.Token != "theirs" {
		t.Fatalf("a closed Store must not take the lock: %+v, %v", held, err)
	}
}
