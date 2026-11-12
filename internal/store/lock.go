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

// ErrClosed is returned by TakeOver once Close has been called, and is
// matched (errors.Is) by ChangePassphrase's error once the vault key has
// been zeroed (ZeroKey or Close).
var ErrClosed = errors.New("the vault has been closed")

// LockInfo is the contents of vlt.lock: who has the vault open, and since
// when. Token identifies the holder, so a Store only removes its own lock.
type LockInfo struct {
	Host  string    `json:"host"`
	User  string    `json:"user"`
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
	Token string    `json:"token"`
}

// inUseError reports the lock found by acquireLock. Lock is zero if the lock
// file could not be parsed, or this user may not read it. Only lock reads
// it, through errors.As; it never reaches a caller of the package.
type inUseError struct {
	Lock LockInfo
}

func (e *inUseError) Error() string { return "the vault is open elsewhere" }

// lock records in vlt.lock that this Store has the vault open, until Close.
// Open and Create call it before returning the Store. If another copy of
// vlt holds the lock, the Store is still returned, without the lock, and
// OpenElsewhere reports who holds it. Any other failure (the lock cannot be
// written, or read for a reason that says nothing about who holds it) is
// logged and ignored: the lock only warns, and must not keep the user out
// of their vault.
func (s *Store) lock(ctx context.Context) {
	err := s.acquireLock(ctx, false)
	var inUse *inUseError
	switch {
	case errors.As(err, &inUse):
		s.lockMu.Lock()
		s.elsewhere = &inUse.Lock
		s.lockMu.Unlock()
	case err != nil:
		s.log.Warn("cannot record that the vault is open", "err", err)
	}
}

// OpenElsewhere reports whether another copy of vlt had the vault open when
// this Store was opened or created, and if so its lock: holder is zero if
// the lock file could not be parsed, or this user may not read it. A lock
// left on this computer by a copy of vlt that is no longer running (after a
// crash) is taken over without asking, and is not reported. Until TakeOver
// succeeds, this Store does not hold the lock, and Close leaves it alone.
func (s *Store) OpenElsewhere() (holder LockInfo, elsewhere bool) {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.elsewhere == nil {
		return LockInfo{}, false
	}
	return *s.elsewhere, true
}

// TakeOver takes over the lock OpenElsewhere reports, as when the user
// chooses to open the vault anyway: vlt.lock then records this Store, and
// Close releases it. It blocks on the vault's storage, and may be called
// from any goroutine.
//
// TakeOver and Close exclude each other, so a closed Store never leaves a
// lock behind: once Close has been called, TakeOver returns ErrClosed and
// writes nothing; a TakeOver already running when Close is called finishes
// first, and Close then releases the lock it took.
func (s *Store) TakeOver(ctx context.Context) error {
	if !s.beginTakeOver() {
		return fmt.Errorf("store.TakeOver: %w", ErrClosed)
	}
	defer s.takeOvers.Done()
	if err := s.acquireLock(ctx, true); err != nil {
		return fmt.Errorf("store.TakeOver: %w", err)
	}
	s.lockMu.Lock()
	s.elsewhere = nil
	s.lockMu.Unlock()
	return nil
}

// beginTakeOver counts a takeover in, unless Close has been called. Counting
// under lockMu, which Close takes to set closing before it waits, means
// takeOvers is never added to while Close waits on it.
func (s *Store) beginTakeOver() bool {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	if s.closing {
		return false
	}
	s.takeOvers.Add(1)
	return true
}

// refuseTakeOvers makes every later TakeOver fail. Close calls it at once,
// then waits on takeOvers for those already running before it releases the
// lock.
func (s *Store) refuseTakeOvers() {
	s.lockMu.Lock()
	s.closing = true
	s.lockMu.Unlock()
}

// acquireLock records in vlt.lock that this Store has the vault open. If
// another copy of vlt holds the lock it returns an *inUseError, unless force
// is set, in which case the lock is taken over. A lock left by a copy of vlt
// on this computer that is no longer running (after a crash) is taken over
// without asking.
func (s *Store) acquireLock(ctx context.Context, force bool) error {
	if err := ctx.Err(); err != nil {
		return err // callers wrap
	}
	mine, err := newLockInfo()
	if err != nil {
		return err // callers wrap
	}
	data, err := json.Marshal(mine)
	if err != nil {
		return err // callers wrap
	}

	if !force {
		taken, err := s.tryLock(data, &mine)
		if err != nil || taken {
			return err
		}
	}

	if err := s.fs.WriteFileAtomic(lockFile, data); err != nil {
		return err // callers wrap
	}
	s.setLockToken(mine.Token)
	return nil
}

// lockAttempts bounds retries when the lock is released between a failed
// create and the read that follows it.
const lockAttempts = 3

// tryLock creates the lock if it is free. It reports true if it took the
// lock, false (and no error) if the existing lock is stale and may be
// replaced, and an *inUseError if someone else holds it.
func (s *Store) tryLock(data []byte, mine *LockInfo) (bool, error) {
	var lost error
	for range lockAttempts {
		err := s.fs.WriteFileExclusive(lockFile, data)
		switch {
		case err == nil:
			s.setLockToken(mine.Token)
			return true, nil
		case !errors.Is(err, fs.ErrExist):
			return false, err // callers wrap
		}
		held, err := s.readLock()
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // released since; try to create it again
		case errors.Is(err, errBadLock), errors.Is(err, fs.ErrPermission):
			// Someone holds it, but who can't be read: a damaged lock, or
			// another user's that this one may not read.
			return false, &inUseError{}
		case errors.Is(err, ErrConnectionLost):
			// The next call reconnects; try again for a real answer.
			lost = err
			continue
		case err != nil:
			// A file system error says nothing about whether the vault is
			// open elsewhere.
			return false, err
		case isStale(&held, mine):
			s.log.Info("taking over a stale vault lock", "host", held.Host, "pid", held.PID, "since", held.Since)
			return false, nil
		default:
			return false, &inUseError{Lock: held}
		}
	}
	if lost != nil {
		return false, lost
	}
	return false, fmt.Errorf("%s keeps appearing and disappearing", lockFile)
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
		return LockInfo{}, fmt.Errorf("%w: %w", errBadLock, err)
	}
	return info, nil
}

// errBadLock marks a lock file that exists but does not parse.
var errBadLock = errors.New("parse " + lockFile)

// releaseLock removes vlt.lock if this Store still holds it. A lock taken
// over by another copy of vlt is left alone. To make "check, then remove"
// safe against another copy writing its lock in between, the lock is first
// renamed aside atomically, then checked. The file moved aside is deleted
// only once it is known to be ours: a release can run late (Close is slow on
// a network folder), and by then the lock may be a newer one, this process's
// next session's or another computer's. A lock that cannot be read is
// renamed back; another copy's is put back unless a newer lock has appeared
// meanwhile, in which case it is left aside rather than deleted.
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
	data, err := s.fs.ReadFile(aside)
	if err != nil {
		s.log.Warn("cannot read the vault lock to release it; putting it back", "err", err)
		s.restoreLock(aside)
		return
	}
	var held LockInfo
	if json.Unmarshal(data, &held) == nil && held.Token == token {
		s.removeAside(aside)
		return
	}
	s.log.Info("vault lock was taken over; leaving it", "host", held.Host, "pid", held.PID)
	switch err := s.fs.WriteFileExclusive(lockFile, data); {
	case err == nil:
		s.removeAside(aside)
	case errors.Is(err, fs.ErrExist):
		s.log.Warn("another copy locked the vault while its lock was set aside; leaving the earlier lock aside",
			"host", held.Host, "pid", held.PID, "file", aside)
	default:
		s.log.Warn("cannot put back the other copy's vault lock", "err", err)
		s.restoreLock(aside)
	}
}

// restoreLock renames a lock set aside by releaseLock back into place, or
// leaves it aside if that fails.
func (s *Store) restoreLock(aside string) {
	if err := s.fs.Rename(aside, lockFile); err != nil {
		s.log.Warn("cannot put back the vault lock; leaving it aside", "file", aside, "err", err)
	}
}

// removeAside deletes the lock file releaseLock set aside.
func (s *Store) removeAside(aside string) {
	if err := s.fs.Remove(aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Warn("cannot remove the released vault lock", "err", err)
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
