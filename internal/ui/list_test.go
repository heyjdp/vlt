package ui

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

// F05: a secret selected from the list (not created via the Add buttons)
// must update or leave the list when saved or deleted.

func TestMainView_SelectThenDeleteRemovesRowAndClearsSelection(t *testing.T) {
	f := newView(t,
		testutil.PasswordSecret("a-gone", "v", "work"),
		testutil.PasswordSecret("b-kept", "v", "work"),
	)
	sl, dv := f.view.list, f.view.detail

	f.selectRow(0)
	if f.shown() == nil || f.shown().Name != "a-gone" {
		t.Fatalf("selecting row 0 should show a-gone, got %+v", f.shown())
	}
	dv.deleteSecret(f.shown())

	if len(sl.filtered) != 1 || sl.filtered[0].Name != "b-kept" {
		t.Fatalf("deleted secret still listed: %d rows", len(sl.filtered))
	}
	// If the selection had not been cleared, re-selecting row 0 would be a
	// no-op and the detail view would still show the deleted secret.
	f.selectRow(0)
	if f.shown() == nil || f.shown().Name != "b-kept" {
		t.Fatalf("re-selecting row 0 should show b-kept, got %+v", f.shown())
	}
}

func TestMainView_SelectThenEditUpdatesFilteredRow(t *testing.T) {
	f := newView(t,
		testutil.PasswordSecret("github", "old", "work"),
		testutil.PasswordSecret("bank", "x", "personal"),
	)
	sl, dv := f.view.list, f.view.detail

	test.Type(sl.searchEntry, "git")
	if len(sl.filtered) != 1 {
		t.Fatalf("search should narrow to one row, got %d", len(sl.filtered))
	}
	f.selectRow(0)
	sel := f.shown()
	edited := *sel
	edited.Value = "new"
	edited.Tags = []string{"ci"}
	dv.saveSecret(&edited, false, saveButton(), func() { *sel = edited })

	if len(sl.filtered) != 1 || sl.filtered[0].Value != "new" || len(sl.filtered[0].Tags) != 1 {
		t.Fatalf("edited row not updated in the filtered list: %+v", sl.filtered)
	}
}

func TestMainView_NewSecretSavedDuringSearchAppearsIfItMatches(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("github", "x", "work"))
	sl := f.view.list

	test.Type(sl.searchEntry, "git")
	f.view.detail.saveSecret(testutil.PasswordSecret("gitlab", "y", "work"), true, saveButton(), func() {})

	if len(sl.filtered) != 2 {
		t.Fatalf("a new matching secret should appear in the filtered view, got %d rows", len(sl.filtered))
	}
}

func TestSecretList_MatchesLegacyNamesByPath(t *testing.T) {
	test.NewTempApp(t)
	q := &queue{}
	sl := listOver(t, q.runner())
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
	// The first load is queued: it reads the vault while "gone" still exists.
	f, q := newHeldView(t, testutil.PasswordSecret("gone", "v", "work"), testutil.PasswordSecret("kept", "v", "work"))
	f.view.list.Remove(&store.Secret{Name: "gone", Group: "work"})
	q.flush()

	if len(f.listed()) != 1 || f.listed()[0].Name != "kept" {
		t.Fatalf("a delete made during the first load must survive it, got %d rows", len(f.listed()))
	}
}

func TestSecretList_SaveDuringFirstLoadSurvivesLoad(t *testing.T) {
	// The first load is queued: as if it read the vault before the save below.
	f, q := newHeldView(t, testutil.PasswordSecret("on-disk", "v", "work"))
	f.view.list.Upsert(testutil.PasswordSecret("saved-meanwhile", "v", "work"))
	q.flush()

	names := map[string]bool{}
	for _, sec := range f.listed() {
		names[sec.Name] = true
	}
	if !names["on-disk"] || !names["saved-meanwhile"] {
		t.Fatalf("expected both the loaded and the concurrently saved secret, got %v", names)
	}
}

func TestMainView_LateDeleteDoesNotClearAnotherSecretsPanel(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("a-gone", "v", "work"), testutil.PasswordSecret("b-shown", "v", "work"))
	q := f.hold()

	f.selectRow(0)
	f.view.detail.deleteSecret(f.shown()) // queued
	f.selectRow(1)                        // user moves on to b-shown
	q.flush()

	if f.shown() == nil || f.shown().Name != "b-shown" {
		t.Fatalf("a late delete must not clear the panel of another secret, panel shows %+v", f.shown())
	}
	if len(f.listed()) != 1 {
		t.Fatalf("deleted secret should still leave the list, got %d rows", len(f.listed()))
	}
}

func TestMainView_ImportReplacingOpenSecretRefreshesItsForm(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("token", "old", "keys"))

	f.selectRow(0)
	replacement := testutil.FileSecret("token", "token", "eA==", "keys")
	if err := f.store.Update(context.Background(), replacement); err != nil {
		t.Fatal(err)
	}
	f.view.files.importDone(replacement, importedFile{size: 1})

	if f.shown() == nil || f.shown().Type != store.TypeFile {
		t.Fatalf("the open form should be re-rendered for the imported file, got %+v", f.shown())
	}
}

func TestMainView_EditThatLeavesSearchClearsHighlight(t *testing.T) {
	tagged := testutil.PasswordSecret("github", "x", "work")
	tagged.Tags = []string{"ci"}
	f := newView(t, tagged)
	sl := f.view.list

	test.Type(sl.searchEntry, "ci") // matches by tag
	f.selectRow(0)
	sel := f.shown()
	edited := *sel
	edited.Tags = nil // the edit removes the tag the search matched
	f.view.detail.saveSecret(&edited, false, saveButton(), func() { *sel = edited })

	if len(sl.filtered) != 0 {
		t.Fatalf("edited secret should leave the filtered view, got %d rows", len(sl.filtered))
	}
	if sl.selected != nil {
		t.Fatal("the highlight should clear when the edited secret leaves the filtered view")
	}
}

func TestSecretList_GroupsShowOneLabelPerDirectory(t *testing.T) {
	test.NewTempApp(t)
	sl := listOver(t, inlineRunner())
	sl.secrets = []*store.Secret{
		{Name: "a", Group: "Work"}, {Name: "b", Group: "personal"}, {Name: "c", Group: "work"},
	}

	if got := sl.Groups(); !slices.Equal(got, []string{"personal", "Work"}) {
		t.Fatalf("Groups() = %q, want one label per directory, sorted ignoring case", got)
	}
}

func TestMatchGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		typed  string
		groups []string
		want   string
	}{
		{"different case", "work", []string{"Personal Finance", "Work"}, "Work"},
		{"slug of an existing label", "personal-finance", []string{"Personal Finance", "Work"}, "Personal Finance"},
		{"exact", "Work", []string{"Personal Finance", "Work"}, "Work"},
		{"new group", "Travel", []string{"Personal Finance", "Work"}, "Travel"},
		{"group saved by an earlier version", "Personal Finance", []string{"personal-finance"}, "personal-finance"},
		{"empty, no default group", "", []string{"Work"}, ""},
		{"empty joins an existing default group", "", []string{"Default"}, "Default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := matchGroup(tt.typed, tt.groups); got != tt.want {
				t.Fatalf("matchGroup(%q, %q) = %q, want %q", tt.typed, tt.groups, got, tt.want)
			}
		})
	}
}

func TestSecretList_SearchMatchesGroupBySpellingOrDirectory(t *testing.T) {
	test.NewTempApp(t)
	sl := listOver(t, inlineRunner())
	sl.secrets = []*store.Secret{
		{Name: "bank", Group: "Personal Finance"}, {Name: "old", Group: "personal-finance"}, {Name: "gh", Group: "Work"},
	}

	for _, query := range []string{"personal-finance", "personal finance", "Finance"} {
		sl.filter(query)
		if len(sl.filtered) != 2 {
			t.Errorf("search %q found %d secrets, want both spellings of the group", query, len(sl.filtered))
		}
	}
}

// A vault that can't be listed says why, rather than looking empty, and
// Retry loads it once the problem is gone.
func TestSecretList_ALoadErrorIsShownWithRetry(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix folder permissions that apply to this user")
	}
	f := newView(t, testutil.PasswordSecret("github", "pw", "work"))
	if err := os.Chmod(f.dir, 0o300); err != nil { // no read: the folder can't be listed
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.dir, 0o700) })

	f.view.list.Refresh()
	panel := f.view.list.Content()
	if !visibleText(panel, "Couldn't load the vault") || !visibleText(panel, "permission denied") {
		t.Fatal("a failed load should say so, and why")
	}

	if err := os.Chmod(f.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	test.Tap(visibleButton(t, panel, "Retry"))
	if len(f.listed()) != 1 || visibleText(panel, "Couldn't load the vault") {
		t.Fatal("Retry should load the vault and clear the message")
	}
}

// Secret files the list skips (damaged, from another vault, misplaced) are
// counted under the list, with their paths a click away.
func TestSecretList_SkippedFilesAreCountedWithDetails(t *testing.T) {
	f, q := newHeldView(t, testutil.PasswordSecret("github", "pw", "work"))
	for _, name := range []string{"broken.age", "other.age"} {
		if err := os.WriteFile(filepath.Join(f.dir, "work", name), []byte("not age"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	q.flush() // the first load

	panel := f.view.list.Content()
	if len(f.listed()) != 1 || !visibleText(panel, "2 secret files couldn't be read") {
		t.Fatal("skipped files should be counted under the list")
	}
	if shownButton(panel, "Retry") != nil {
		t.Fatal("Retry belongs to a failed load, not to skipped files")
	}
	test.Tap(visibleButton(t, panel, "Details…"))
	if text := dialogText(t, f.window); !strings.Contains(text, "work/broken.age") || !strings.Contains(text, "work/other.age") {
		t.Fatalf("details should list the skipped files, got %q", text)
	}
}

func TestSecretList_NoNoticeWhenEverythingLoads(t *testing.T) {
	f := newView(t, testutil.PasswordSecret("github", "pw", "work"))
	panel := f.view.list.Content()
	if shownButton(panel, "Retry") != nil || shownButton(panel, "Details…") != nil || visibleText(panel, "couldn't") {
		t.Fatal("no notice should show when the vault loads cleanly")
	}
}

func TestUnreadableText(t *testing.T) {
	t.Parallel()
	for paths, want := range map[string]string{
		"work/a.age":            "1 secret file couldn't be read.",
		"work/a.age work/b.age": "2 secret files couldn't be read.",
		"work/":                 "1 secret file or folder couldn't be read.",
		"home/ work/a.age":      "2 secret files or folders couldn't be read.",
	} {
		if got := unreadableText(strings.Fields(paths)); got != want {
			t.Errorf("unreadableText(%s) = %q, want %q", paths, got, want)
		}
	}
}

// listOver returns a secret list on its own, without a vault, for a test
// that gives it its secrets; work runs on run.
func listOver(t *testing.T, run runner) *SecretList {
	t.Helper()
	return newSecretList(&mainViewDeps{window: test.NewTempWindow(t, nil), log: quietLog(), ops: testOps(t, run)},
		func(*store.Secret) {})
}

// Nothing shows under the list until a load says something.
func TestSecretList_NoNoticeBeforeTheFirstLoad(t *testing.T) {
	f, q := newHeldView(t, testutil.PasswordSecret("on-disk", "v", "work"))
	sl := f.view.list // its first load is queued
	if shownButton(sl.Content(), "Retry") != nil || shownButton(sl.Content(), "Details…") != nil {
		t.Fatal("the notice should stay hidden while the first load runs")
	}
	q.flush()
}

// A second load while one runs would drop the first's replay queue, so a
// save made meanwhile could vanish; Retry waits for the load to finish.
func TestSecretList_RetryWaitsForTheLoadInFlight(t *testing.T) {
	f, q := newHeldView(t, testutil.PasswordSecret("on-disk", "v", "work"))
	sl := f.view.list // its first load is queued
	if !sl.retry.Disabled() {
		t.Fatal("Retry should be disabled while a load runs")
	}
	test.Tap(sl.retry)
	sl.Upsert(testutil.PasswordSecret("saved", "v", "work"))
	q.flush()

	if !slices.ContainsFunc(sl.secrets, func(s *store.Secret) bool { return s.Name == "saved" }) {
		t.Fatalf("a save made during the load was lost: %v", sl.secrets)
	}
	if sl.retry.Disabled() {
		t.Fatal("Retry should be enabled again once the load finishes")
	}
}
