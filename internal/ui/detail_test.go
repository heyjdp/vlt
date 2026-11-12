package ui

import (
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
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
	sl := listOver(t, inlineRunner())
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
	return containedEntries(dv.Content())
}

// openOnly shows a main view over a vault holding sec, with sec selected.
func openOnly(t *testing.T, sec *store.Secret) *viewFixture {
	t.Helper()
	f := newView(t, sec)
	f.selectRow(0)
	return f
}

func TestDetailView_RecordFormMasksSecretsAndSavesEveryField(t *testing.T) {
	rec := testutil.RecordSecret("bank", "me", "pw", "https://bank.example", "work")
	rec.TOTPSecret = "JBSWY3DPEHPK3PXP"
	f := openOnly(t, rec)

	// name, URL, username, password, TOTP, tags, notes
	e := formEntries(f.view.detail)
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
	test.Tap(findButton(f.panel(), "Save"))

	got, err := f.store.Get(t.Context(), "bank", "work")
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
}

func TestDetailView_PasswordFormMasksAndSavesValue(t *testing.T) {
	f := openOnly(t, testutil.PasswordSecret("gh", "ghp_old", "work"))

	e := formEntries(f.view.detail) // name, value, tags, notes
	value := e[1]
	if !value.Password || value.Text != "ghp_old" {
		t.Fatal("the password value must be shown masked")
	}
	value.SetText("ghp_new")
	test.Tap(findButton(f.panel(), "Save"))

	got, err := f.store.Get(t.Context(), "gh", "work")
	if err != nil {
		t.Fatal(err)
	}
	if got.Value != "ghp_new" {
		t.Fatalf("value not saved: %q", got.Value)
	}
}

func TestDetailView_ExistingFormHasDeleteAndEnabledCopy(t *testing.T) {
	f := openOnly(t, testutil.RecordSecret("bank", "me", "pw", "", "work"))
	form := f.panel()

	if findButton(form, "Delete") == nil {
		t.Fatal("an existing secret should offer Delete")
	}
	for _, label := range []string{"Copy Password", "Copy Username"} {
		if b := findButton(form, label); b == nil || b.Disabled() {
			t.Fatalf("%q should be enabled for a saved secret", label)
		}
	}
}

func TestDetailView_NewFormHasNoDeleteAndDisabledCopy(t *testing.T) {
	f := openOnly(t, testutil.PasswordSecret("gh", "x", "work"))

	f.view.detail.ShowNewRecord()
	form := f.panel()

	if findButton(form, "Delete") != nil {
		t.Fatal("a new, unsaved secret must not offer Delete")
	}
	for _, label := range []string{"Copy Password", "Copy Username"} {
		if b := findButton(form, label); b == nil || !b.Disabled() {
			t.Fatalf("%q should be disabled until the secret is saved", label)
		}
	}
	if name := formEntries(f.view.detail)[0]; name.Disabled() {
		t.Fatal("a new secret's name must be editable")
	}
}

func TestDetailView_CopyButtonsCopyTheRightValue(t *testing.T) {
	f := openOnly(t, testutil.RecordSecret("bank", "the-user", "the-pass", "", "work"))
	clip := f.clip

	test.Tap(findButton(f.panel(), "Copy Username"))
	if clip.Content() != "the-user" {
		t.Fatalf("Copy Username copied %q", clip.Content())
	}
	test.Tap(findButton(f.panel(), "Copy Password"))
	if clip.Content() != "the-pass" {
		t.Fatalf("Copy Password copied %q", clip.Content())
	}
}

func TestDetailView_NewSecretUsesTheExistingGroupSpelling(t *testing.T) {
	f := openOnly(t, testutil.PasswordSecret("gh", "x", "Personal Finance"))

	f.view.detail.ShowNewPassword()
	form := f.panel()
	e := formEntries(f.view.detail) // name, value, tags, notes
	e[0].SetText("bank")
	e[1].SetText("pw")
	findType[*widget.SelectEntry](form).SetText("personal finance")
	test.Tap(findButton(form, "Save"))

	got, err := f.store.Get(t.Context(), "bank", "personal-finance")
	if err != nil {
		t.Fatal(err)
	}
	if got.Group != "Personal Finance" {
		t.Fatalf("group = %q, want the existing spelling", got.Group)
	}
}

// Saving a secret whose file another copy of vlt has since given to a
// differently named secret says which secret now holds it.
func TestDetailView_SaveOverAFileNowHeldByAnotherSecretNamesIt(t *testing.T) {
	f := openOnly(t, testutil.PasswordSecret("GitHub Key", "x", "work"))
	other, err := store.Open(t.Context(), f.dir, testPassphrase, quietLog(), testKDF)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Delete(t.Context(), "GitHub Key", "work"); err != nil {
		t.Fatal(err)
	}
	if err := other.Create(t.Context(), testutil.PasswordSecret("github-key", "y", "work")); err != nil {
		t.Fatal(err)
	}

	test.Tap(findButton(f.panel(), "Save"))

	want := `Couldn't save the secret: its file is now held by the secret "github-key"`
	if text := dialogText(t, f.window); !strings.Contains(text, want) {
		t.Fatalf("error dialog says %q, want %q", text, want)
	}
}
