package ui

import (
	"bytes"
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// viewFixture is a main view built directly over a temporary vault, as
// App builds one for a session, but without an App or an unlock. Its work
// runs inline unless held (see hold), its timers fire only by hand, its
// clock is set by the test, and its file choosers are scripted.
type viewFixture struct {
	view   *mainView
	store  *store.Store
	dir    string
	window fyne.Window
	clip   fyne.Clipboard
	status *widget.Label
	pick   *scriptedPicker
	timers *fakeClock
	clock  *clockAt
	// held, when set, keeps work queued until the test flushes it.
	held *queue
	// cancel ends the session's context, as locking does.
	cancel context.CancelFunc
	// activity counts the input the view reported to the auto-lock.
	activity int
}

// newView builds a main view over a new vault holding secrets, and lets
// its first load finish.
func newView(t *testing.T, secrets ...*store.Secret) *viewFixture {
	t.Helper()
	return buildView(t, nil, secrets...)
}

// newHeldView builds a main view whose work, from its first load on,
// waits in the returned queue.
func newHeldView(t *testing.T, secrets ...*store.Secret) (*viewFixture, *queue) {
	t.Helper()
	q := &queue{}
	return buildView(t, q, secrets...), q
}

func buildView(t *testing.T, held *queue, secrets ...*store.Secret) *viewFixture {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, secrets...)
	s, err := store.Open(context.Background(), dir, testPassphrase, quietLog(), testKDF)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	f := &viewFixture{
		store: s, dir: dir, window: test.NewTempWindow(t, nil), clip: a.Clipboard(),
		status: widget.NewLabel(""), pick: &scriptedPicker{}, timers: &fakeClock{},
		clock: &clockAt{t: time.Now().UTC()}, held: held, cancel: cancel,
	}
	run := runner{spawn: func(work func()) {
		if f.held != nil {
			f.held.pending = append(f.held.pending, work)
			return
		}
		work()
	}}.withContext(ctx)
	reportActivity := func() { f.activity++ }
	ops := newOperations(f.window, run, f.status, reportActivity, nil, nil, f.pick)
	clipboard := newClipboardClearer(f.clip, f.status, func() time.Duration { return 30 * time.Second }, f.timers.factory)
	f.view = newMainView(&mainViewDeps{
		window: f.window, store: s, ops: ops, copy: clipboard.Copy, status: f.status, log: quietLog(),
		timers: f.timers.factory, now: f.clock.now, activity: reportActivity, footer: widget.NewLabel(""),
		lock: f.lock,
		settings: func() *SettingsScreen {
			return NewSettingsScreen(f.window, settingsAt(a, dir), s, f.status, quietLog(), ops)
		},
	})
	f.window.SetContent(f.view.content())
	return f
}

// hold makes the view's work from now on wait in the returned queue.
func (f *viewFixture) hold() *queue {
	f.held = &queue{}
	return f.held
}

// lock does to the view what locking does: the session's context ends
// (dropping work in flight) and the view is wiped.
func (f *viewFixture) lock() {
	f.cancel()
	f.view.wipe()
}

// selectRow selects row i of the list, which shows that secret.
func (f *viewFixture) selectRow(i int) { f.view.list.list.Select(i) }

// panel is the detail panel's contents.
func (f *viewFixture) panel() fyne.CanvasObject { return f.view.detail.Content() }

// shown is the saved secret the detail panel shows, or nil.
func (f *viewFixture) shown() *store.Secret { return f.view.detail.current }

// listed is the secrets the list holds, filtered or not.
func (f *viewFixture) listed() []*store.Secret { return f.view.list.secrets }

// tap taps the button labelled text in the view (the header's actions,
// the detail panel's buttons).
func (f *viewFixture) tap(t *testing.T, text string) {
	t.Helper()
	b := findButton(f.view.content(), text)
	if b == nil {
		t.Fatalf("no %q button in the main view", text)
	}
	test.Tap(b)
}

// importFile imports r through the Import File button and the file
// chooser.
func (f *viewFixture) importFile(t *testing.T, r fyne.URIReadCloser) {
	t.Helper()
	f.tap(t, "Import File")
	f.pick.answerFile(t, r, nil)
}

func TestMainView_ShowsTheVaultsSecrets(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("gh", "x", "work"), testutil.PasswordSecret("bank", "y", "personal"))
	if got := len(f.listed()); got != 2 {
		t.Fatalf("expected 2 secrets in the list, got %d", got)
	}
	f.selectRow(0)
	if f.shown() == nil || f.shown().Name != "bank" {
		t.Fatalf("selecting a row should show its secret, got %+v", f.shown())
	}
}

func TestMainView_SaveFromListSelectionUpdatesList(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("gh", "old", "work"))
	f.selectRow(0)
	formEntries(f.view.detail)[1].SetText("new")
	f.tap(t, "Save")

	if f.listed()[0].Value != "new" {
		t.Fatal("saving a secret selected from the list should update the list")
	}
}

func TestMainView_SaveNewSecretAddsItToList(t *testing.T) {
	f := newView(t)
	f.tap(t, "Add Password")
	e := formEntries(f.view.detail) // name, value, tags, notes
	e[0].SetText("new-one")
	e[1].SetText("v")
	f.tap(t, "Save")

	if len(f.listed()) != 1 || f.listed()[0].Name != "new-one" {
		t.Fatalf("a newly created secret should be added to the list, got %d entries", len(f.listed()))
	}
}

func TestMainView_DeleteRemovesSecretFromList(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("gone", "v", "work"), testutil.PasswordSecret("kept", "v", "work"))
	f.selectRow(0)
	f.tap(t, "Delete")
	test.Tap(dialogButton(t, f.window, "Yes"))

	if len(f.listed()) != 1 || f.listed()[0].Name != "kept" {
		t.Fatalf("deleted secret should leave the list, got %d entries", len(f.listed()))
	}
}

func TestMainView_ImportAddsSecretToList(t *testing.T) {
	f := newView(t)
	f.view.files.importSecret(testutil.FileSecret("id_rsa", "id_rsa", "eA==", "keys"), importedFile{size: 1})

	if len(f.listed()) != 1 || f.listed()[0].Name != "id_rsa" {
		t.Fatal("an imported file should be added to the list")
	}
}

func TestMainView_SettingsShowInTheDetailPanel(t *testing.T) {
	f := newView(t)
	f.tap(t, "Settings")
	if findButton(f.panel(), "Change Passphrase") == nil {
		t.Fatal("Settings should show in the detail panel")
	}
}

func TestMainView_InputInARenderedFormIsActivity(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("gh", "x", "work"))
	f.selectRow(0)
	before := f.activity
	test.Type(firstEditableEntry(f.panel()), "more")
	if f.activity == before {
		t.Fatal("typing in a form the detail panel rendered should count as activity")
	}
}

// One wipe releases everything the view holds of the vault.
func TestMainView_WipeReleasesEverythingItHolds(t *testing.T) {
	f := newView(t, totpRecord(rfcSecret))
	f.clock.t = time.Unix(59, 0).UTC()
	loaded := f.listed()[0]
	f.selectRow(0)
	entries := formEntries(f.view.detail)
	code := findDeep(f.panel(), func(o fyne.CanvasObject) bool {
		l, ok := o.(*widget.Label)
		return ok && l.Text == "287 082"
	}).(*widget.Label)
	tick := f.timers.active(time.Second)
	data := []byte("decoded file content")
	f.view.files.exportDecoded("n", "out.txt", data) // its save dialog left open

	f.view.wipe()

	if !bytes.Equal(data, make([]byte, len(data))) {
		t.Error("a pending export must be wiped")
	}
	if !tick.stopped || code.Text != "" {
		t.Error("the one-time code must stop and be blanked")
	}
	for i, e := range entries {
		if e.Text != "" {
			t.Errorf("form entry %d still shows %q", i, e.Text)
		}
	}
	if loaded.Name != "" || loaded.Password != "" || f.listed() != nil {
		t.Errorf("the loaded secrets must be emptied: %+v", *loaded)
	}
	if n := len(f.view.tracker.seen); n != 0 {
		t.Errorf("the view still tracks %d widgets", n)
	}
}
