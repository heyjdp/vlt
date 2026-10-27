package ui

import (
	"runtime"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/testutil"
)

func newTestClearer(t *testing.T, ttl time.Duration) (*clipboardClearer, fyne.Clipboard, *fakeClock) {
	t.Helper()
	a := test.NewTempApp(t)
	clock := &fakeClock{}
	ttlNow := ttl
	c := newClipboardClearer(a.Clipboard(), widget.NewLabel(""), func() time.Duration { return ttlNow }, clock.factory)
	return c, a.Clipboard(), clock
}

// tick fires the clearer's pending one-second timer.
func tick(t *testing.T, clock *fakeClock) {
	t.Helper()
	timer := clock.active(time.Second)
	if timer == nil {
		t.Fatal("expected a pending countdown tick")
	}
	timer.stopped = true
	timer.f()
}

func TestClipboard_ClearsAfterTTL(t *testing.T) {
	c, clip, clock := newTestClearer(t, 3*time.Second)

	c.Copy("secret-a")
	tick(t, clock)
	tick(t, clock)
	if clip.Content() != "secret-a" {
		t.Fatal("cleared before the TTL elapsed")
	}
	tick(t, clock)

	if clip.Content() != "" {
		t.Fatalf("clipboard not cleared after the TTL: %q", clip.Content())
	}
	if clock.active(time.Second) != nil {
		t.Fatal("no timer should remain after clearing")
	}
}

func TestClipboard_SecondCopyGetsTheFullTTL(t *testing.T) {
	c, clip, clock := newTestClearer(t, 3*time.Second)

	c.Copy("secret-a")
	tick(t, clock)
	tick(t, clock)
	oldTick := clock.active(time.Second)
	c.Copy("secret-b") // two seconds into A's countdown
	if !oldTick.stopped {
		t.Fatal("a new copy must stop the previous countdown")
	}

	// A's pending tick was already running when B was copied: it must not
	// clear B.
	oldTick.f()
	if clip.Content() != "secret-b" {
		t.Fatal("the earlier copy's countdown cleared the later copy")
	}
	tick(t, clock)
	tick(t, clock)
	if clip.Content() != "secret-b" {
		t.Fatal("B was cleared before its own full TTL")
	}
	tick(t, clock)
	if clip.Content() != "" {
		t.Fatal("B should be cleared after its full TTL")
	}
}

func TestClipboard_LeavesUnrelatedContentAlone(t *testing.T) {
	c, clip, clock := newTestClearer(t, 2*time.Second)

	c.Copy("secret-a")
	clip.SetContent("something the user copied elsewhere")
	tick(t, clock)
	tick(t, clock)

	if clip.Content() != "something the user copied elsewhere" {
		t.Fatalf("unrelated clipboard content was wiped: %q", clip.Content())
	}
}

func TestClipboard_ClearWipesImmediatelyAndStops(t *testing.T) {
	c, clip, clock := newTestClearer(t, 30*time.Second)

	c.Copy("secret-a")
	pending := clock.active(time.Second)
	c.Clear()

	if clip.Content() != "" {
		t.Fatal("Clear should wipe vlt's value immediately")
	}
	if !pending.stopped || clock.active(time.Second) != nil {
		t.Fatal("Clear should stop the countdown")
	}
	pending.f() // a tick already in flight
	if clip.Content() != "" {
		t.Fatal("a stale tick must not act after Clear")
	}
}

func TestClipboard_ClearLeavesUnrelatedContentAlone(t *testing.T) {
	c, clip, _ := newTestClearer(t, 30*time.Second)

	c.Copy("secret-a")
	clip.SetContent("other")
	c.Clear()

	if clip.Content() != "other" {
		t.Fatal("Clear must not wipe content vlt did not put there")
	}
}

func TestClipboard_NonPositiveTTLUsesDefault(t *testing.T) {
	c, _, _ := newTestClearer(t, 0)
	c.Copy("x")
	if want := int(defaultClipboardTTL / time.Second); c.remaining != want {
		t.Fatalf("remaining = %d, want %d", c.remaining, want)
	}
}

func TestApp_LockClearsCopiedSecret(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "ghp_secret", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir, ClipboardTTLs: 30}, quietLog(), inlineRunner(), noTimers)
	unlock(app)

	app.detailView.copyValue("ghp_secret")
	if a.Clipboard().Content() != "ghp_secret" {
		t.Fatal("copy did not reach the clipboard")
	}
	app.Lock()

	if a.Clipboard().Content() != "" {
		t.Fatal("locking must clear a secret vlt copied")
	}
}

func TestApp_ClipboardTTLChangeAppliesToNextCopy(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir, ClipboardTTLs: 30}, quietLog(), inlineRunner(), noTimers)
	unlock(app)

	app.config.ClipboardTTLs = 5 // Settings slider
	app.detailView.copyValue("x")

	if app.clipboard.remaining != 5 {
		t.Fatalf("new TTL should apply to the next copy, countdown is %ds", app.clipboard.remaining)
	}
	app.Lock()
}

func TestClipboard_EmptyReadIsTreatedAsOurs(t *testing.T) {
	c, clip, _ := newTestClearer(t, 30*time.Second)

	c.Copy("secret-a")
	clip.SetContent("") // as if the OS clipboard could not be read
	if !c.wipeIfOurs() {
		t.Fatal("an unreadable (empty) clipboard must be cleared, not skipped")
	}
}

func TestClipboard_EmptyValueIsNotCopied(t *testing.T) {
	c, clip, clock := newTestClearer(t, 30*time.Second)
	clip.SetContent("keep")

	c.Copy("")

	if clip.Content() != "keep" || len(clock.timers) != 0 {
		t.Fatal("copying an empty value must not touch the clipboard or start a countdown")
	}
}

// TestClipboard_RealTimersLeaveNoGoroutines checks the spec criterion that a
// cleared countdown leaves nothing running, using real timers.
func TestClipboard_RealTimersLeaveNoGoroutines(t *testing.T) {
	a := test.NewTempApp(t)
	c := newClipboardClearer(a.Clipboard(), widget.NewLabel(""), func() time.Duration { return time.Second }, realTimers)
	before := runtime.NumGoroutine()

	c.Copy("secret-a")
	c.Clear()
	time.Sleep(1500 * time.Millisecond) // past the first tick, had it not been stopped

	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines grew from %d to %d after Clear", before, after)
	}
	if a.Clipboard().Content() != "" {
		t.Fatal("Clear should have wiped the clipboard")
	}
}

func TestApp_QuitClearsCopiedSecret(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "ghp_secret", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir, ClipboardTTLs: 30}, quietLog(), inlineRunner(), noTimers)
	unlock(app)

	app.detailView.copyValue("ghp_secret")
	app.quit()

	if a.Clipboard().Content() != "" {
		t.Fatal("quitting must clear a secret vlt copied")
	}
}
