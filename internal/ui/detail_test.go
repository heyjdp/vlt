package ui

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
	"github.com/yourorg/vlt/internal/testutil"
)

func TestParseTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"dev", []string{"dev"}},
		{"dev, ci ,prod", []string{"dev", "ci", "prod"}},
		{"a,,b, ,", []string{"a", "b"}},
		{"\u00a0dev\u2003,\tci\n", []string{"dev", "ci"}}, // non-breaking space, em space, tab, newline
	}
	for _, tt := range tests {
		if got := parseTags(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseTags(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSecretList_GroupsAreSortedAndDistinct(t *testing.T) {
	test.NewTempApp(t)
	sl := NewSecretList(test.NewTempWindow(t, nil), nil, &DetailView{}, quietLog(), inlineRunner())
	sl.secrets = []*store.Secret{
		{Name: "a", Group: "work"}, {Name: "b", Group: "personal"}, {Name: "c", Group: "work"},
	}

	if got := sl.Groups(); !reflect.DeepEqual(got, []string{"personal", "work"}) {
		t.Fatalf("Groups() = %v", got)
	}
}

// formEntries returns the plain and password entries of the detail form, in
// order (the group selector and the notes box are a SelectEntry and a
// multi-line Entry; notes is included last).
func formEntries(dv *DetailView) []*widget.Entry {
	var entries []*widget.Entry
	for _, obj := range dv.Content().Objects {
		if e, ok := obj.(*widget.Entry); ok {
			entries = append(entries, e)
		}
	}
	return entries
}

// openOnly unlocks a vault holding sec and selects it.
func openOnly(t *testing.T, sec *store.Secret) *App {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, sec)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)
	app.secretList.list.Select(0)
	return app
}

func TestDetailView_RecordFormMasksSecretsAndSavesEveryField(t *testing.T) {
	rec := testutil.RecordSecret("bank", "me", "pw", "https://bank.example", "work")
	rec.TOTPSecret = "JBSWY3DPEHPK3PXP"
	app := openOnly(t, rec)

	// name, URL, username, password, TOTP, tags, notes
	e := formEntries(app.detailView)
	if len(e) != 7 {
		t.Fatalf("expected 7 entries in a record form, got %d", len(e))
	}
	name, url, user, pass, totp, tags, notes := e[0], e[1], e[2], e[3], e[4], e[5], e[6]
	if !pass.Password || !totp.Password || url.Password || user.Password {
		t.Fatal("password and TOTP secret must be masked; URL and username must not")
	}
	if !name.Disabled() {
		t.Fatal("an existing secret's name must be read-only")
	}

	// Change every field, so a field that is not stored keeps its old value
	// and fails the comparison below.
	url.SetText("https://bank.example/login")
	user.SetText("me2")
	pass.SetText("pw2")
	totp.SetText("NEWTOTPSECRET234")
	tags.SetText("money, home")
	notes.SetText("rotated")
	test.Tap(findButton(app.detailView.Content(), "Save"))

	got, err := app.store.Get(t.Context(), "bank", "work")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"URL": got.URL, "Username": got.Username, "Password": got.Password, "TOTP": got.TOTPSecret, "Notes": got.Notes}
	for field, value := range map[string]string{"URL": "https://bank.example/login", "Username": "me2", "Password": "pw2", "TOTP": "NEWTOTPSECRET234", "Notes": "rotated"} {
		if want[field] != value {
			t.Errorf("%s not saved: got %q, want %q", field, want[field], value)
		}
	}
	if !reflect.DeepEqual(got.Tags, []string{"money", "home"}) {
		t.Errorf("tags not saved: %q", got.Tags)
	}
	app.Lock()
}

func TestDetailView_PasswordFormMasksAndSavesValue(t *testing.T) {
	app := openOnly(t, testutil.PasswordSecret("gh", "ghp_old", "work"))

	e := formEntries(app.detailView) // name, value, tags, notes
	value := e[1]
	if !value.Password || value.Text != "ghp_old" {
		t.Fatal("the password value must be shown masked")
	}
	value.SetText("ghp_new")
	test.Tap(findButton(app.detailView.Content(), "Save"))

	got, err := app.store.Get(t.Context(), "gh", "work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "ghp_new" {
		t.Fatalf("value not saved: %q", got.Value)
	}
	app.Lock()
}

func TestDetailView_ExistingFormHasDeleteAndEnabledCopy(t *testing.T) {
	app := openOnly(t, testutil.RecordSecret("bank", "me", "pw", "", "work"))
	form := app.detailView.Content()

	if findButton(form, "Delete") == nil {
		t.Fatal("an existing secret should offer Delete")
	}
	for _, label := range []string{"Copy Password", "Copy Username"} {
		if b := findButton(form, label); b == nil || b.Disabled() {
			t.Fatalf("%q should be enabled for a saved secret", label)
		}
	}
	app.Lock()
}

func TestDetailView_NewFormHasNoDeleteAndDisabledCopy(t *testing.T) {
	app := openOnly(t, testutil.PasswordSecret("gh", "x", "work"))

	app.detailView.ShowNewRecord()
	form := app.detailView.Content()

	if findButton(form, "Delete") != nil {
		t.Fatal("a new, unsaved secret must not offer Delete")
	}
	for _, label := range []string{"Copy Password", "Copy Username"} {
		if b := findButton(form, label); b == nil || !b.Disabled() {
			t.Fatalf("%q should be disabled until the secret is saved", label)
		}
	}
	if name := formEntries(app.detailView)[0]; name.Disabled() {
		t.Fatal("a new secret's name must be editable")
	}
	app.Lock()
}

func TestDetailView_CopyButtonsCopyTheRightValue(t *testing.T) {
	app := openOnly(t, testutil.RecordSecret("bank", "the-user", "the-pass", "", "work"))
	clip := app.fyneApp.Clipboard()

	test.Tap(findButton(app.detailView.Content(), "Copy Username"))
	if clip.Content() != "the-user" {
		t.Fatalf("Copy Username copied %q", clip.Content())
	}
	test.Tap(findButton(app.detailView.Content(), "Copy Password"))
	if clip.Content() != "the-pass" {
		t.Fatalf("Copy Password copied %q", clip.Content())
	}
	app.Lock()
}
