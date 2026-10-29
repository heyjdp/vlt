package ui

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
	"github.com/heyjdp/vlt/internal/testutil"
)

func TestCountText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		shown, total int
		want         string
	}{
		{0, 0, "0 secrets"},
		{1, 1, "1 secret"},
		{4, 4, "4 secrets"},
		{1, 4, "1 of 4 secrets"},
		{0, 4, "0 of 4 secrets"},
	}
	for _, tt := range tests {
		if got := countText(tt.shown, tt.total); got != tt.want {
			t.Errorf("countText(%d, %d) = %q, want %q", tt.shown, tt.total, got, tt.want)
		}
	}
}

func TestUserMessage(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("store.Open: %w", fmt.Errorf("store.Exists %q: %w", "nas:/v", errors.New("ssh: Connection refused")))
	if got := userMessage(err); got != "ssh: Connection refused" {
		t.Fatalf("userMessage = %q", got)
	}
}

// Not parallel: it sets HOME.
func TestDisplayLocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir reads USERPROFILE")
	}
	t.Setenv("HOME", "/home/me")
	for in, want := range map[string]string{
		"/home/me/vault":               "~/vault",
		"/home/me":                     "/home/me",
		"/home/meg/vault":              "/home/meg/vault",
		"/srv/vault":                   "/srv/vault",
		"me@192.0.2.10:/home/me/vault": "me@192.0.2.10:/home/me/vault",
	} {
		if got := displayLocation(in); got != want {
			t.Errorf("displayLocation(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTypeNamesAndIcons(t *testing.T) {
	test.NewTempApp(t) // the theme icons read the current app
	seen := map[fyne.Resource]bool{}
	for _, typ := range []store.SecretType{store.TypePassword, store.TypeRecord, store.TypeFile} {
		if typeName(typ) == string(typ) {
			t.Errorf("type %q has no display name", typ)
		}
		icon := typeIcon(typ)
		if icon == nil || seen[icon] {
			t.Errorf("type %q needs an icon of its own", typ)
		}
		seen[icon] = true
	}
}

func TestColumnLayout_WrappedTextIsNeverClipped(t *testing.T) {
	test.NewTempApp(t)
	long := muted(strings.Repeat("a passphrase cannot be recovered ", 8))
	short := widget.NewLabel("one line")
	// The app swaps screens into a window that already has its size; that
	// is when a VBox measured the label before it had a width.
	w := test.NewTempWindow(t, nil)
	w.Resize(fyne.NewSize(800, 600))
	w.SetContent(centeredCard(300, short, long))

	// The label wraps to several lines at the column's width; it must be
	// given the height of all of them.
	oneLine := short.MinSize().Height
	if long.Size().Height < 2*oneLine {
		t.Fatalf("wrapped label is %.0f tall; one line is %.0f", long.Size().Height, oneLine)
	}
	if long.Size().Height < long.MinSize().Height {
		t.Fatalf("wrapped label clipped: %.0f of %.0f", long.Size().Height, long.MinSize().Height)
	}
	if got, want := long.Size().Width, float32(300-2*cardPadding); got != want {
		t.Fatalf("column width = %.0f, want %.0f", got, want)
	}
}

func TestColumnLayout_SkipsHiddenObjects(t *testing.T) {
	test.NewTempApp(t)
	a, hidden, b := widget.NewLabel("a"), widget.NewLabel("hidden"), widget.NewLabel("b")
	hidden.Hide()
	c := container.New(&columnLayout{width: 200}, a, hidden, b)
	test.NewTempWindow(t, container.NewCenter(c))
	if b.Position().Y > a.Position().Y+a.MinSize().Height+2*theme.Padding() {
		t.Fatal("a hidden object must not take up space")
	}
	if got, want := c.MinSize().Height, a.MinSize().Height+b.MinSize().Height+theme.Padding(); got != want {
		t.Fatalf("column min height = %.1f, want %.1f (hidden objects excluded)", got, want)
	}
}

func TestListRow_ShowsNameGroupAndType(t *testing.T) {
	test.NewTempApp(t)
	row := newListRow()
	updateListRow(row, testutil.RecordSecret("Bank", "me", "pw", "https://bank.example", "Personal Finance"))

	lines := row.(*fyne.Container).Objects[0].(*fyne.Container).Objects
	if got := lines[0].(*widget.Label).Text; got != "Bank" {
		t.Fatalf("row name = %q", got)
	}
	if got := lines[1].(*widget.Label).Text; got != "Personal Finance · Record" {
		t.Fatalf("row detail = %q", got)
	}
	icon := findType[*widget.Icon](row)
	if icon == nil || icon.Resource != typeIcon(store.TypeRecord) {
		t.Fatal("the row should show the record icon")
	}
}

func TestSecretList_CountFollowsTheSearch(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir,
		testutil.PasswordSecret("github", "x", "work"),
		testutil.PasswordSecret("wifi", "y", "home"))
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	if got := app.secretList.count.Text; got != "2 secrets" {
		t.Fatalf("count = %q", got)
	}
	test.Type(app.secretList.searchEntry, "git")
	if got := app.secretList.count.Text; got != "1 of 2 secrets" {
		t.Fatalf("count while searching = %q", got)
	}
	list := app.secretList
	app.Lock()
	if list.count.Text != "" {
		t.Fatalf("count still shows %q after lock", list.count.Text)
	}
}
