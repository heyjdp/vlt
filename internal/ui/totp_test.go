package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
	"github.com/heyjdp/vlt/internal/totp"
)

// rfcSecret is RFC 6238's SHA-1 test seed in base32; at Unix time 59 its
// six-digit code is 287082.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestSpacedCode(t *testing.T) {
	t.Parallel()
	for code, want := range map[string]string{"287082": "287 082", "1234567": "123 4567", "07081804": "0708 1804"} {
		if got := spacedCode(code); got != want {
			t.Errorf("spacedCode(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestCheckTOTPSecret(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"", "  ", rfcSecret, "otpauth://totp/x?secret=" + rfcSecret} {
		if err := checkTOTPSecret(ok); err != nil {
			t.Errorf("checkTOTPSecret(%q) = %v", ok, err)
		}
	}
	if err := checkTOTPSecret("not base32!"); err == nil || !errors.Is(err, totp.ErrNotBase32) {
		t.Errorf("checkTOTPSecret(bad) = %v", err)
	}
}

// totpRecord is a saved record whose TOTP secret is secret.
func totpRecord(secret string) *store.Secret {
	rec := testutil.RecordSecret("bank", "me", "pw", "https://bank.example", "work")
	rec.TOTPSecret = secret
	return rec
}

// clockAt is a settable clock.
type clockAt struct{ t time.Time }

func (c *clockAt) now() time.Time { return c.t }

// openRecordAt59 shows rec in a main view whose clock is at Unix time 59
// (the RFC secret's code is 287082, changing in 1 s), with hand-fired
// timers.
func openRecordAt59(t *testing.T, rec *store.Secret) *viewFixture {
	t.Helper()
	f := newView(t, rec)
	f.clock.t = time.Unix(59, 0).UTC()
	f.selectRow(0)
	return f
}

// hasLabel reports whether a label in obj shows text.
func hasLabel(obj fyne.CanvasObject, text string) bool {
	return findDeep(obj, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Text == text
	}) != nil
}

// codeAt is the RFC secret's code at unix seconds, spaced as shown.
func codeAt(t *testing.T, unix int64) string {
	t.Helper()
	k, err := totp.Parse(rfcSecret)
	if err != nil {
		t.Fatal(err)
	}
	return spacedCode(k.Code(time.Unix(unix, 0)))
}

func TestTOTP_ShowsTheCodeAndTicksToTheNext(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	timers, clock := f.timers, f.clock
	panel := f.panel()
	if !hasLabel(panel, "287 082") || !hasLabel(panel, "Changes in 1 s") {
		t.Fatal("the record should show its current code and when it changes")
	}
	tick := timers.active(time.Second)
	if tick == nil {
		t.Fatal("the code should tick")
	}
	clock.t = time.Unix(60, 0)
	tick.f()
	if !hasLabel(panel, codeAt(t, 60)) || !hasLabel(panel, "Changes in 30 s") {
		t.Fatal("at the period boundary the next code should show")
	}
	if timers.active(time.Second) == tick || timers.active(time.Second) == nil {
		t.Fatal("each tick should schedule the next")
	}
}

func TestTOTP_TicksAlignToWholeSeconds(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	timers, clock := f.timers, f.clock
	clock.t = time.Unix(59, 750_000_000)
	f.view.detail.ShowSecret(f.shown())
	if timers.active(250*time.Millisecond) == nil {
		t.Fatal("the first tick should come at the next whole second")
	}
}

func TestTOTP_CopyCodeCopiesTheDigits(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	test.Tap(findButton(f.panel(), "Copy Code"))
	if got := f.clip.Content(); got != "287082" {
		t.Fatalf("clipboard = %q, want 287082", got)
	}
	if !strings.Contains(f.status.Text, "clears in") {
		t.Fatal("a copied code should be cleared like any copied secret")
	}
}

func TestTOTP_StopsWhenThePanelChangesOrTheVaultLocks(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	timers, clock := f.timers, f.clock
	tick := timers.active(time.Second)

	f.view.detail.ShowNewPassword()
	if !tick.stopped {
		t.Fatal("showing another form should stop the code's ticking")
	}
	clock.t = time.Unix(60, 0)
	tick.f() // a tick already queued when the panel changed
	if hasLabel(f.panel(), codeAt(t, 60)) || timers.active(time.Second) != nil {
		t.Fatal("a stale tick must not update or reschedule anything")
	}

	f.view.list.list.Unselect(0)
	f.selectRow(0)
	tick = timers.active(time.Second)
	if tick == nil {
		t.Fatal("selecting the record again should restart the code")
	}
	f.lock()
	if !tick.stopped {
		t.Fatal("locking should stop the code's ticking")
	}
}

func TestTOTP_AnUnreadableSecretIsReported(t *testing.T) {
	f := openRecordAt59(t, totpRecord("not base32!"))
	timers := f.timers
	panel := f.panel()
	if findDeep(panel, func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && strings.HasPrefix(l.Text, "Can't make codes: the TOTP secret is not valid base32")
	}) == nil {
		t.Fatal("an unreadable secret should be explained")
	}
	if findButton(panel, "Copy Code") != nil || timers.active(time.Second) != nil {
		t.Fatal("no code to copy and nothing to tick")
	}
}

func TestTOTP_NoPanelWithoutASecretOrOnANewRecord(t *testing.T) {
	f := openRecordAt59(t, totpRecord(""))
	if hasLabel(f.panel(), "One-Time Code") {
		t.Fatal("a record without a TOTP secret shows no code")
	}
	f.view.detail.ShowNewRecord()
	if hasLabel(f.panel(), "One-Time Code") {
		t.Fatal("a new record shows no code until it is saved")
	}
}

func TestTOTP_AnInvalidSecretIsNotSaved(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	e := formEntries(f.view.detail)
	totpEntry := e[4]
	totpEntry.SetText("not base32!")
	test.Tap(findButton(f.panel(), "Save"))

	if top := f.window.Canvas().Overlays().Top(); top == nil || !hasLabel(top, "Couldn't save the secret: the TOTP secret is not valid base32 (letters A–Z and digits 2–7)") {
		t.Fatal("saving an invalid TOTP secret should explain why it was refused")
	}
	got, err := f.store.Get(context.Background(), "bank", "work")
	if err != nil || got.TOTPSecret != rfcSecret {
		t.Fatalf("the stored secret changed: %q, %v", got.TOTPSecret, err)
	}
}

func TestTOTP_ALinkIsSavedTrimmedAndShowsItsCode(t *testing.T) {
	f := openRecordAt59(t, totpRecord(""))
	link := "otpauth://totp/Bank:me?secret=" + rfcSecret + "&issuer=Bank"
	formEntries(f.view.detail)[4].SetText("  " + link + "\n")
	test.Tap(findButton(f.panel(), "Save"))

	got, err := f.store.Get(context.Background(), "bank", "work")
	if err != nil || got.TOTPSecret != link {
		t.Fatalf("stored %q, %v; want the trimmed link", got.TOTPSecret, err)
	}
	if !hasLabel(f.panel(), "287 082") {
		t.Fatal("saving a TOTP secret should show its code straight away")
	}

	formEntries(f.view.detail)[4].SetText("")
	test.Tap(findButton(f.panel(), "Save"))
	if hasLabel(f.panel(), "One-Time Code") {
		t.Fatal("removing the TOTP secret should remove the code")
	}
}

func TestTOTP_TheCountdownRoundsUp(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	clock := f.clock
	clock.t = time.Unix(30, 250_000_000) // 29.75 s left
	f.view.detail.ShowSecret(f.shown())
	if !hasLabel(f.panel(), "Changes in 30 s") {
		t.Fatal("29.75 s left should read 30 s, as a tick just after the second does")
	}
}

func TestTOTP_LockingWipesTheShownCode(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	label := findDeep(f.panel(), func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Text == "287 082"
	}).(*widget.Label)
	copyCode := findButton(f.panel(), "Copy Code")
	f.lock()
	if label.Text != "" {
		t.Fatalf("the code label still shows %q after the lock", label.Text)
	}
	// The key is wiped: a cached button can no longer make the real code.
	copyCode.OnTapped()
	if f.clip.Content() == "287082" {
		t.Fatal("the wiped key still made the code")
	}
}

func TestTOTP_AnUnchangedUnreadableSecretDoesNotBlockSaving(t *testing.T) {
	f := openRecordAt59(t, totpRecord("on my phone!"))
	formEntries(f.view.detail)[3].SetText("new password")
	test.Tap(findButton(f.panel(), "Save"))

	got, err := f.store.Get(context.Background(), "bank", "work")
	if err != nil || got.Password != "new password" || got.TOTPSecret != "on my phone!" {
		t.Fatalf("stored %+v, %v; the other fields should save", got, err)
	}
}

func TestTOTP_SavingWithoutChangingTheSecretKeepsTheForm(t *testing.T) {
	f := openRecordAt59(t, totpRecord(rfcSecret))
	name := formEntries(f.view.detail)[0]
	formEntries(f.view.detail)[6].SetText("new notes")
	test.Tap(findButton(f.panel(), "Save"))
	if formEntries(f.view.detail)[0] != name {
		t.Fatal("a save that leaves the TOTP secret alone should not rebuild the form")
	}
}

func TestTOTP_ASaveFinishingAfterMovingOnDoesNotPullTheRecordBack(t *testing.T) {
	f := newView(t, totpRecord(""), testutil.PasswordSecret("aaa", "x", "work"))
	q := f.hold()
	bank := 1 // the list is sorted: aaa, bank
	f.selectRow(bank)

	formEntries(f.view.detail)[4].SetText(rfcSecret)
	test.Tap(findButton(f.panel(), "Save"))
	f.selectRow(0) // move on while the save runs
	q.flush()
	if f.shown() == nil || f.shown().Name != "aaa" || hasLabel(f.panel(), "One-Time Code") {
		t.Fatal("the save should not show the record again over the secret the user moved to")
	}
}
