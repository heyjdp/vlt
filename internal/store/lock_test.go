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

func TestLock_AnUnreadableLockIsInUse(t *testing.T) {
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
	if secrets, err := s.List(context.Background()); err != nil || len(secrets) != 0 {
		t.Fatalf("List = %v, %v", secrets, err)
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
