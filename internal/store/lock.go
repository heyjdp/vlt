package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"time"
)

// lockFile marks a vault as open. It is advisory: it warns when the same
// vault is opened by two copies of vlt at once (on two computers sharing a
// server vault, or twice on one computer), because the last of two saves to
// the same secret wins. It holds no secrets, only who has the vault open.
const lockFile = "vlt.lock"

// ErrVaultInUse is matched (errors.Is) by the *InUseError AcquireLock
// returns when another copy of vlt has the vault open.
var ErrVaultInUse = errors.New("the vault is open elsewhere")

// LockInfo is the contents of vlt.lock: who has the vault open, and since
// when. Token identifies the holder, so a Store only removes its own lock.
type LockInfo struct {
	Host  string    `json:"host"`
	User  string    `json:"user"`
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
	Token string    `json:"token"`
}

// InUseError reports the lock found by AcquireLock. Lock is zero if the lock
// file could not be parsed.
type InUseError struct {
	Lock LockInfo
}

func (e *InUseError) Error() string {
	if e.Lock.Host == "" {
		return "the vault is open elsewhere (its lock file is unreadable)"
	}
	return fmt.Sprintf("the vault is open on %s (user %s, process %d) since %s",
		e.Lock.Host, e.Lock.User, e.Lock.PID, e.Lock.Since.UTC().Format("2 Jan 2006 15:04 UTC"))
}

// Is makes errors.Is(err, ErrVaultInUse) match.
func (e *InUseError) Is(target error) bool { return target == ErrVaultInUse }

// AcquireLock records in vlt.lock that this Store has the vault open, until
// Close. If another copy of vlt holds the lock it returns an *InUseError,
// unless force is set, in which case the lock is taken over. A lock left by
// a copy of vlt on this computer that is no longer running (after a crash)
// is taken over without asking.
func (s *Store) AcquireLock(ctx context.Context, force bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store.AcquireLock: %w", err)
	}
	mine, err := newLockInfo()
	if err != nil {
		return fmt.Errorf("store.AcquireLock: %w", err)
	}
	data, err := json.Marshal(mine)
	if err != nil {
		return fmt.Errorf("store.AcquireLock: %w", err)
	}

	if !force {
		taken, err := s.tryLock(data, &mine)
		if err != nil || taken {
			return err
		}
	}

	if err := s.fs.WriteFileAtomic(lockFile, data); err != nil {
		return fmt.Errorf("store.AcquireLock: %w", err)
	}
	s.setLockToken(mine.Token)
	return nil
}

// lockAttempts bounds retries when the lock is released between a failed
// create and the read that follows it.
const lockAttempts = 3

// tryLock creates the lock if it is free. It reports true if it took the
// lock, false (and no error) if the existing lock is stale and may be
// replaced, and an *InUseError if someone else holds it.
func (s *Store) tryLock(data []byte, mine *LockInfo) (bool, error) {
	for range lockAttempts {
		err := s.fs.WriteFileExclusive(lockFile, data)
		switch {
		case err == nil:
			s.setLockToken(mine.Token)
			return true, nil
		case !errors.Is(err, fs.ErrExist):
			return false, fmt.Errorf("store.AcquireLock: %w", err)
		}
		held, err := s.readLock()
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // released since; try to create it again
		case err != nil:
			return false, &InUseError{}
		case isStale(&held, mine):
			s.log.Info("taking over a stale vault lock", "host", held.Host, "pid", held.PID, "since", held.Since)
			return false, nil
		default:
			return false, &InUseError{Lock: held}
		}
	}
	return false, fmt.Errorf("store.AcquireLock: %s keeps appearing and disappearing", lockFile)
}

// isStale reports whether a lock was left on this computer by a process
// that is no longer running, or by this process (an earlier session).
// Computers are told apart by host name only, so two computers with the
// same name are treated as one.
func isStale(held, mine *LockInfo) bool {
	if held.Host != mine.Host {
		return false
	}
	return held.PID == mine.PID || !processAlive(held.PID)
}

func (s *Store) setLockToken(token string) {
	s.lockMu.Lock()
	s.lockToken = token
	s.lockMu.Unlock()
}

// readLock reads and parses vlt.lock.
func (s *Store) readLock() (LockInfo, error) {
	data, err := s.fs.ReadFile(lockFile)
	if err != nil {
		return LockInfo{}, err
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return LockInfo{}, fmt.Errorf("parse %s: %w", lockFile, err)
	}
	return info, nil
}

// releaseLock removes vlt.lock if this Store still holds it. A lock taken
// over by another copy of vlt is left alone. To make "check, then remove"
// safe against another copy writing its lock in between, the lock is first
// renamed aside atomically, then checked: if it turns out not to be ours, it
// is put back, unless a newer lock has appeared meanwhile.
func (s *Store) releaseLock() {
	s.lockMu.Lock()
	token := s.lockToken
	s.lockToken = ""
	s.lockMu.Unlock()
	if token == "" {
		return
	}
	aside := tmpPrefix + "lock-" + token
	if err := s.fs.Rename(lockFile, aside); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("cannot release the vault lock", "err", err)
		}
		return
	}
	defer func() {
		if err := s.fs.Remove(aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("cannot remove the released vault lock", "err", err)
		}
	}()
	data, err := s.fs.ReadFile(aside)
	if err != nil {
		s.log.Warn("cannot read the vault lock to release it", "err", err)
		return
	}
	var held LockInfo
	if json.Unmarshal(data, &held) == nil && held.Token == token {
		return
	}
	s.log.Info("vault lock was taken over; leaving it", "host", held.Host, "pid", held.PID)
	if err := s.fs.WriteFileExclusive(lockFile, data); err != nil && !errors.Is(err, fs.ErrExist) {
		s.log.Warn("cannot put back the other copy's vault lock", "err", err)
	}
}

// newLockInfo describes this process.
func newLockInfo() (LockInfo, error) {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown host"
	}
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		return LockInfo{}, err
	}
	return LockInfo{
		Host:  host,
		User:  name,
		PID:   os.Getpid(),
		Since: time.Now().UTC(),
		Token: hex.EncodeToString(token),
	}, nil
}
