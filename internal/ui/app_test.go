package ui

import (
	"context"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/testutil"
)

// inlineRunner runs background work immediately on the caller's goroutine.
// With fyne/test's inline fyne.Do, app tests are deterministic.
func inlineRunner() runner {
	return runner{spawn: func(f func()) { f() }}
}

// queue is a runner that holds work until flush, so tests can observe the
// UI while work is "in flight" and prove it is not done on the caller.
type queue struct{ pending []func() }

func (q *queue) runner() runner {
	return runner{spawn: func(f func()) { q.pending = append(q.pending, f) }}
}

func (q *queue) flush() {
	for len(q.pending) > 0 {
		f := q.pending[0]
		q.pending = q.pending[1:]
		f()
	}
}

// newVaultWith creates a vault in dir holding secrets, with a cheap work
// factor.
func newVaultWith(t *testing.T, dir string, secrets ...*store.Secret) {
	t.Helper()
	s, err := store.Create(dir, "correct horse battery", quietLog(), store.WithWorkFactor(10))
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	defer s.Close()
	for _, sec := range secrets {
		if err := s.Create(context.Background(), sec); err != nil {
			t.Fatalf("Create %q: %v", sec.Name, err)
		}
	}
}

func unlock(app *App) {
	test.Type(app.unlockScreen.entry, "correct horse battery")
	test.Tap(app.unlockScreen.unlockBtn)
}

func TestApp_UnlockLoadsSecretsIntoList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir,
		testutil.PasswordSecret("gh", "x", "work"),
		testutil.PasswordSecret("bank", "y", "personal"),
	)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)

	unlock(app)

	if app.secretList == nil {
		t.Fatal("expected the main view after unlocking")
	}
	if got := len(app.secretList.secrets); got != 2 {
		t.Fatalf("expected 2 secrets in the list, got %d", got)
	}
	app.Lock()
}

func TestApp_UnlockRunsOffTheMainGoroutine(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), q.runner(), noTimers)
	q.flush() // the Exists check

	unlock(app)

	if app.store != nil {
		t.Fatal("unlock must not open the vault synchronously in the tap handler")
	}
	if !app.unlockScreen.unlockBtn.Disabled() || app.unlockScreen.progress.Hidden {
		t.Fatal("unlock screen should show progress and disable input while opening")
	}

	q.flush()
	if app.store == nil || app.secretList == nil {
		t.Fatal("expected the vault to be open once background work completes")
	}
	app.Lock()
	q.flush()
}

func TestApp_StaleUnlockIsDiscardedAfterVaultSwitch(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), q.runner(), noTimers)
	q.flush()

	unlock(app)
	// Before the open completes, the vault directory changes and the app relocks.
	app.config.VaultDir = t.TempDir()
	app.Lock()
	q.flush()

	if app.store != nil {
		t.Fatal("an unlock result for a superseded screen must be discarded")
	}
	if app.createScreen == nil {
		t.Fatal("expected the create screen for the newly selected empty directory")
	}
}

func TestApp_SaveFromListSelectionUpdatesList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "old", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)

	sec := app.secretList.secrets[0]
	edited := *sec
	edited.Value = "new"
	app.detailView.saveSecret(&edited, false, saveButton(), func() { *sec = edited })

	if app.secretList.secrets[0].Value != "new" {
		t.Fatal("saving a secret selected from the list should update the list")
	}
	app.Lock()
}

// unlockQueued unlocks with a queued runner and runs the open, leaving the
// initial list load queued.
func unlockQueued(t *testing.T, app *App, q *queue) {
	t.Helper()
	unlock(app)
	open := q.pending[0]
	q.pending = q.pending[1:]
	open()
	if app.secretList == nil {
		t.Fatal("expected the main view after the open completes")
	}
}

func TestApp_SaveNewSecretAddsItToList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)

	sec := testutil.PasswordSecret("new-one", "v", "work")
	app.detailView.saveSecret(sec, true, saveButton(), func() {})

	if len(app.secretList.secrets) != 1 || app.secretList.secrets[0].Name != "new-one" {
		t.Fatalf("a newly created secret should be added to the list, got %d entries", len(app.secretList.secrets))
	}
	app.Lock()
}

func TestApp_DeleteRemovesSecretFromList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gone", "v", "work"), testutil.PasswordSecret("kept", "v", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)

	var target *store.Secret
	for _, s := range app.secretList.secrets {
		if s.Name == "gone" {
			target = s
		}
	}
	app.detailView.deleteSecret(target)

	if len(app.secretList.secrets) != 1 || app.secretList.secrets[0].Name != "kept" {
		t.Fatalf("deleted secret should leave the list, got %d entries", len(app.secretList.secrets))
	}
	app.Lock()
}

func TestApp_ImportAddsSecretToList(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)

	app.detailView.fileOps.importSecret(testutil.FileSecret("id_rsa", "id_rsa", "eA==", "keys"), 1)

	if len(app.secretList.secrets) != 1 || app.secretList.secrets[0].Name != "id_rsa" {
		t.Fatal("an imported file should be added to the list")
	}
	app.Lock()
}

func TestApp_SaveCompletingAfterLockIsDropped(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), q.runner(), noTimers)
	q.flush()
	unlockQueued(t, app, q)
	q.flush() // initial list load

	app.detailView.saveSecret(testutil.PasswordSecret("late", "v", "work"), true, saveButton(), func() {})
	app.Lock()
	q.flush()

	if got := app.statusBar.Text; got != "Locked" {
		t.Fatalf("a save finishing after lock must not touch the UI; status bar says %q", got)
	}
}

func TestApp_LockCancelsInitialListLoad(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gh", "x", "work"))
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), q.runner(), noTimers)
	q.flush()
	unlockQueued(t, app, q)
	list := app.secretList

	app.Lock()
	q.flush()

	if len(list.secrets) != 0 {
		t.Fatal("a list load cancelled by Lock must not populate the list")
	}
}

func TestApp_CreateRunsOffTheMainGoroutine(t *testing.T) {
	a := test.NewTempApp(t)
	dir := filepath.Join(t.TempDir(), "vault")
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), q.runner(), noTimers)
	q.flush()

	test.Type(app.createScreen.entry, "correct horse battery")
	test.Type(app.createScreen.confirm, "correct horse battery")
	test.Tap(app.createScreen.createBtn)

	if exists, _ := store.Exists(dir); exists {
		t.Fatal("create must not run synchronously in the tap handler")
	}
	if !app.createScreen.createBtn.Disabled() || app.createScreen.progress.Hidden {
		t.Fatal("create screen should show progress and disable input while creating")
	}
	q.flush()
	if app.secretList == nil {
		t.Fatal("expected the main view once the vault is created")
	}
	app.Lock()
	q.flush()
}

func TestSecretList_SaveDuringFirstLoadSurvivesLoad(t *testing.T) {
	test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("on-disk", "v", "work"))
	s, err := store.Open(context.Background(), dir, "correct horse battery", quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	q := &queue{}
	dv := NewDetailView(test.NewTempWindow(t, nil), s, nil, widget.NewLabel(""), quietLog(), q.runner())
	sl := NewSecretList(test.NewTempWindow(t, nil), s, dv, quietLog(), q.runner())

	sl.Refresh() // queued: as if the load read the vault before the save below
	sl.Upsert(testutil.PasswordSecret("saved-meanwhile", "v", "work"))
	q.flush()

	names := map[string]bool{}
	for _, sec := range sl.secrets {
		names[sec.Name] = true
	}
	if !names["on-disk"] || !names["saved-meanwhile"] {
		t.Fatalf("expected both the loaded and the concurrently saved secret, got %v", names)
	}
}

// saveButton is a stand-in for the form's Save button, which saveSecret
// disables while a save is in flight.
func saveButton() *widget.Button { return widget.NewButton("Save", nil) }
