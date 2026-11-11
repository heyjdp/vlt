package store

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"
)

// openedVault creates a local vault and returns it open.
func openedVault(t *testing.T) *Store {
	t.Helper()
	s, err := Create(context.Background(), t.TempDir(), internalPass, quiet(), WithWorkFactor(10))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// plantLock writes a lock as another copy of vlt would.
func plantLock(t *testing.T, s *Store, info *LockInfo) {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.fs.WriteFileAtomic(lockFile, data); err != nil {
		t.Fatal(err)
	}
}

func TestLock_AcquireRecordsThisProcessAndCloseReleasesIt(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	held, err := s.readLock()
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
}

func TestLock_AnotherComputersLockIsReportedAndCanBeTakenOver(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	since := time.Date(2026, 10, 29, 9, 30, 0, 0, time.UTC)
	plantLock(t, s, &LockInfo{Host: "laptop.local", User: "me", PID: 4242, Since: since, Token: "theirs"})

	err := s.AcquireLock(context.Background(), false)
	var inUse *InUseError
	if !errors.As(err, &inUse) || !errors.Is(err, ErrVaultInUse) {
		t.Fatalf("AcquireLock = %v, want *InUseError", err)
	}
	if inUse.Lock.Host != "laptop.local" || inUse.Lock.PID != 4242 || !inUse.Lock.Since.Equal(since) {
		t.Fatalf("reported lock = %+v", inUse.Lock)
	}
	if !strings.Contains(err.Error(), "laptop.local") || !strings.Contains(err.Error(), "29 Oct 2026 09:30 UTC") {
		t.Fatalf("message = %q", err)
	}

	if err := s.AcquireLock(context.Background(), true); err != nil {
		t.Fatalf("forced AcquireLock: %v", err)
	}
	if held, _ := s.readLock(); held.Host == "laptop.local" {
		t.Fatal("force should take the lock over")
	}
}

func TestLock_ALiveProcessOnThisComputerIsInUse(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	host, _ := os.Hostname()
	plantLock(t, s, &LockInfo{Host: host, PID: os.Getppid(), Token: "parent"}) // the test runner: alive

	if err := s.AcquireLock(context.Background(), false); !errors.Is(err, ErrVaultInUse) {
		t.Fatalf("AcquireLock = %v, want ErrVaultInUse", err)
	}
}

func TestLock_AStaleLockOnThisComputerIsTakenOver(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	host, _ := os.Hostname()
	plantLock(t, s, &LockInfo{Host: host, PID: 1 << 30, Token: "crashed"}) // no such process

	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatalf("a crashed copy's lock should be taken over: %v", err)
	}
	if held, _ := s.readLock(); held.PID != os.Getpid() {
		t.Fatalf("lock = %+v", held)
	}
}

func TestLock_ADamagedLockIsInUse(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	if err := s.fs.WriteFileAtomic(lockFile, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	err := s.AcquireLock(context.Background(), false)
	var inUse *InUseError
	if !errors.As(err, &inUse) || inUse.Lock.Host != "" || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("AcquireLock = %v", err)
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

func TestLock_ALockThatCannotBeReadIsAnErrorNotInUse(t *testing.T) {
	t.Parallel()
	for name, readErr := range map[string]error{
		"a dropped connection": ErrConnectionLost,
		"an I/O error":         errors.New("input/output error"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := openedVault(t)
			plantLock(t, s, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
			s.fs = lockReadFails{vaultFS: s.fs, err: readErr, reads: new(int)}

			err := s.AcquireLock(context.Background(), false)
			if errors.Is(err, ErrVaultInUse) || !errors.Is(err, readErr) {
				t.Fatalf("AcquireLock = %v; want the read error, not \"open elsewhere\"", err)
			}
		})
	}
}

// A dropped connection is retried (the next call reconnects), so a lock
// held elsewhere is still found.
func TestLock_AConnectionThatComesBackStillFindsTheLock(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	plantLock(t, s, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	s.fs = lockReadFails{vaultFS: s.fs, err: ErrConnectionLost, times: 1, reads: new(int)}

	err := s.AcquireLock(context.Background(), false)
	var inUse *InUseError
	if !errors.As(err, &inUse) || inUse.Lock.Host != "laptop.local" {
		t.Fatalf("AcquireLock = %v; want the lock held on laptop.local", err)
	}
}

// A lock vlt may not read most likely belongs to another user's copy.
func TestLock_ALockVltMayNotReadIsInUse(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	plantLock(t, s, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	s.fs = lockReadFails{vaultFS: s.fs, err: fs.ErrPermission, reads: new(int)}

	err := s.AcquireLock(context.Background(), false)
	var inUse *InUseError
	if !errors.As(err, &inUse) || inUse.Lock.Host != "" {
		t.Fatalf("AcquireLock = %v; want in use, holder unknown", err)
	}
}

func TestLock_CloseLeavesALockSomeoneElseTookOver(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
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
	s := openedVault(t)
	plantLock(t, s, &LockInfo{Host: "laptop.local", PID: 7, Token: "theirs"})
	s.Close()
	<-s.Closed()
	if _, err := s.readLock(); err != nil {
		t.Fatalf("a Store that never locked removed the lock: %v", err)
	}
}

func TestLock_LockFileIsNotPartOfTheVault(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
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
	s := openedVault(t)
	host, _ := os.Hostname()
	plantLock(t, s, &LockInfo{Host: host, PID: os.Getpid(), Token: "earlier-session"})
	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatalf("this process's own leftover lock should be taken over: %v", err)
	}
	if held, _ := s.readLock(); held.Token == "earlier-session" {
		t.Fatal("the lock should now be this session's")
	}
}

func TestLock_ReleaseLeavesNoTempFiles(t *testing.T) {
	t.Parallel()
	s := openedVault(t)
	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
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
			if err := old.AcquireLock(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			next, err := Open(context.Background(), old.loc.Path, internalPass, quiet(), WithWorkFactor(10))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(next.Close)
			unlock := func() {
				if err := next.AcquireLock(context.Background(), false); err != nil {
					t.Errorf("unlocking again: %v", err)
				}
			}
			if when == "before the release started" {
				unlock() // takes over the old session's lock: same process
			} else {
				old.fs = renameHook{vaultFS: old.fs, after: unlock}
			}

			old.Close()
			<-old.Closed()

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
			if err := s.AcquireLock(context.Background(), false); err != nil {
				t.Fatal(err)
			}
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
	if err := s.AcquireLock(context.Background(), false); err != nil {
		t.Fatal(err)
	}
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
