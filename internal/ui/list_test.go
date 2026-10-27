package ui

import (
	"context"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/testutil"
)

// F05: after a fresh unlock, a secret selected from the list (not created
// via the Add buttons) must update or leave the list when saved or deleted.

func TestApp_SelectThenDeleteRemovesRowAndClearsSelection(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir,
		testutil.PasswordSecret("a-gone", "v", "work"),
		testutil.PasswordSecret("b-kept", "v", "work"),
	)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)
	sl, dv := app.secretList, app.detailView

	sl.list.Select(0)
	if dv.current == nil || dv.current.Name != "a-gone" {
		t.Fatalf("selecting row 0 should show a-gone, got %+v", dv.current)
	}
	dv.deleteSecret(dv.current)

	if len(sl.filtered) != 1 || sl.filtered[0].Name != "b-kept" {
		t.Fatalf("deleted secret still listed: %d rows", len(sl.filtered))
	}
	// If the selection had not been cleared, re-selecting row 0 would be a
	// no-op and the detail view would still show the deleted secret.
	sl.list.Select(0)
	if dv.current == nil || dv.current.Name != "b-kept" {
		t.Fatalf("re-selecting row 0 should show b-kept, got %+v", dv.current)
	}
	app.Lock()
}

func TestApp_SelectThenEditUpdatesFilteredRow(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir,
		testutil.PasswordSecret("github", "old", "work"),
		testutil.PasswordSecret("bank", "x", "personal"),
	)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)
	sl, dv := app.secretList, app.detailView

	test.Type(sl.searchEntry, "git")
	if len(sl.filtered) != 1 {
		t.Fatalf("search should narrow to one row, got %d", len(sl.filtered))
	}
	sl.list.Select(0)
	sel := dv.current
	edited := *sel
	edited.Value = "new"
	edited.Tags = []string{"ci"}
	dv.saveSecret(&edited, false, saveButton(), func() { *sel = edited })

	if len(sl.filtered) != 1 || sl.filtered[0].Value != "new" || len(sl.filtered[0].Tags) != 1 {
		t.Fatalf("edited row not updated in the filtered list: %+v", sl.filtered)
	}
	app.Lock()
}

func TestApp_NewSecretSavedDuringSearchAppearsIfItMatches(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("github", "x", "work"))
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)
	sl := app.secretList

	test.Type(sl.searchEntry, "git")
	app.detailView.saveSecret(testutil.PasswordSecret("gitlab", "y", "work"), true, saveButton(), func() {})

	if len(sl.filtered) != 2 {
		t.Fatalf("a new matching secret should appear in the filtered view, got %d rows", len(sl.filtered))
	}
	app.Lock()
}

func TestSecretList_MatchesLegacyNamesByPath(t *testing.T) {
	test.NewTempApp(t)
	q := &queue{}
	sl := NewSecretList(test.NewTempWindow(t, nil), nil, &DetailView{}, quietLog(), q.runner())
	legacy := &store.Secret{Name: " GitHub ", Group: "work"}
	sl.secrets = []*store.Secret{legacy}

	sl.Upsert(&store.Secret{Name: "github", Group: "work", Value: "new"})
	if len(sl.secrets) != 1 || sl.secrets[0].Value != "new" {
		t.Fatalf("an update to a legacy-named secret must replace its row, got %d rows", len(sl.secrets))
	}
	sl.Remove(&store.Secret{Name: "GITHUB", Group: ""})
	if len(sl.secrets) != 1 {
		t.Fatal("a secret in another group must not be removed")
	}
	sl.Remove(&store.Secret{Name: "github", Group: "Work"})
	if len(sl.secrets) != 0 {
		t.Fatal("removing by name and group must match the stored path")
	}
}

func TestSecretList_DeleteDuringFirstLoadSurvivesLoad(t *testing.T) {
	test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("gone", "v", "work"), testutil.PasswordSecret("kept", "v", "work"))
	s, err := store.Open(context.Background(), dir, "correct horse battery", quietLog())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	q := &queue{}
	dv := NewDetailView(test.NewTempWindow(t, nil), s, nil, widget.NewLabel(""), quietLog(), q.runner())
	sl := NewSecretList(test.NewTempWindow(t, nil), s, dv, quietLog(), q.runner())

	sl.Refresh() // queued: reads the vault while "gone" still exists
	sl.Remove(&store.Secret{Name: "gone", Group: "work"})
	q.flush()

	if len(sl.secrets) != 1 || sl.secrets[0].Name != "kept" {
		t.Fatalf("a delete made during the first load must survive it, got %d rows", len(sl.secrets))
	}
}

func TestApp_LateDeleteDoesNotClearAnotherSecretsPanel(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("a-gone", "v", "work"), testutil.PasswordSecret("b-shown", "v", "work"))
	q := &queue{}
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), q.runner(), noTimers)
	q.flush()
	unlockQueued(t, app, q)
	q.flush()
	sl, dv := app.secretList, app.detailView

	sl.list.Select(0)
	dv.deleteSecret(dv.current) // queued
	sl.list.Select(1)           // user moves on to b-shown
	q.flush()

	if dv.current == nil || dv.current.Name != "b-shown" {
		t.Fatalf("a late delete must not clear the panel of another secret, panel shows %+v", dv.current)
	}
	if len(sl.secrets) != 1 {
		t.Fatalf("deleted secret should still leave the list, got %d rows", len(sl.secrets))
	}
	app.Lock()
	q.flush()
}

func TestApp_ImportReplacingOpenSecretRefreshesItsForm(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.PasswordSecret("token", "old", "keys"))
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)
	dv := app.detailView

	app.secretList.list.Select(0)
	replacement := testutil.FileSecret("token", "token", "eA==", "keys")
	if err := app.store.Update(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	dv.fileOps.importDone(replacement, importedFile{size: 1})

	if dv.current == nil || dv.current.Type != store.TypeFile {
		t.Fatalf("the open form should be re-rendered for the imported file, got %+v", dv.current)
	}
	app.Lock()
}

func TestApp_EditThatLeavesSearchClearsHighlight(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	tagged := testutil.PasswordSecret("github", "x", "work")
	tagged.Tags = []string{"ci"}
	newVaultWith(t, dir, tagged)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers)
	unlock(app)
	sl, dv := app.secretList, app.detailView

	test.Type(sl.searchEntry, "ci") // matches by tag
	sl.list.Select(0)
	sel := dv.current
	edited := *sel
	edited.Tags = nil // the edit removes the tag the search matched
	dv.saveSecret(&edited, false, saveButton(), func() { *sel = edited })

	if len(sl.filtered) != 0 {
		t.Fatalf("edited secret should leave the filtered view, got %d rows", len(sl.filtered))
	}
	if sl.selected != nil {
		t.Fatal("the highlight should clear when the edited secret leaves the filtered view")
	}
	app.Lock()
}
