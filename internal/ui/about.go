package ui

import (
	"fmt"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
)

// projectURL is vlt's home page; bug reports go to its issue tracker.
const projectURL = "https://github.com/heyjdp/vlt"

// buildDetails describes the running build, for the About dialog and bug
// reports. It never includes secrets, paths or names.
type buildDetails struct {
	Version  string // from -ldflags (git describe), or "dev"
	Commit   string // VCS revision, shortened; "-modified" if the tree was dirty
	Built    string // VCS commit time
	Go       string
	Platform string // GOOS/GOARCH
	Fyne     string // fyne.io/fyne/v2 module version
	Vault    string // "local", "on a server", or "" before one is chosen
	Locked   bool
}

// readBuildDetails gathers the build's details. version is main.version.
func readBuildDetails(version string, info *debug.BuildInfo, ok bool) buildDetails {
	d := buildDetails{
		Version:  version,
		Go:       runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
	}
	if d.Version == "" {
		d.Version = "dev"
	}
	if !ok || info == nil {
		return d
	}
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			d.Commit = s.Value
			if len(d.Commit) > 12 {
				d.Commit = d.Commit[:12]
			}
		case "vcs.time":
			d.Built = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if modified && d.Commit != "" { // "-modified" alone would say nothing
		d.Commit += "-modified"
	}
	for _, dep := range info.Deps {
		if dep.Path == "fyne.io/fyne/v2" {
			d.Fyne = dep.Version
		}
	}
	return d
}

// String formats the details as plain text to paste into a bug report.
func (d buildDetails) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "vlt %s\n", d.Version)
	if d.Commit != "" {
		fmt.Fprintf(&b, "Commit: %s", d.Commit)
		if d.Built != "" {
			fmt.Fprintf(&b, " (%s)", d.Built)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Platform: %s, %s", d.Platform, d.Go)
	if d.Fyne != "" {
		fmt.Fprintf(&b, ", Fyne %s", d.Fyne)
	}
	b.WriteString("\n")
	if d.Vault != "" {
		state := "unlocked"
		if d.Locked {
			state = "locked"
		}
		fmt.Fprintf(&b, "Vault: %s, %s\n", d.Vault, state)
	}
	return b.String()
}

// issueURL is a new-issue link with the details already in its body.
func issueURL(details string) string {
	body := "**What happened?**\n\n\n**What did you expect?**\n\n\n**Steps to reproduce**\n\n\n" +
		"**Build**\n```\n" + details + "```\n" +
		"(Please don't paste secrets, passphrases or vault contents.)\n"
	return projectURL + "/issues/new?body=" + url.QueryEscape(body)
}

// aboutDetails returns the details for the current state of the app.
func (a *App) aboutDetails() buildDetails {
	info, ok := debug.ReadBuildInfo()
	d := readBuildDetails(a.version, info, ok)
	if loc, err := store.ParseLocation(a.config.VaultDir); err == nil && a.config.VaultDir != "" {
		d.Vault = "local"
		if loc.IsRemote() {
			d.Vault = "on a server"
		}
	}
	a.mu.Lock()
	d.Locked = a.store == nil
	a.mu.Unlock()
	return d
}

// showAbout shows vlt's version and build, with buttons to copy the details
// and to open a bug report that already contains them. It is reachable from
// every screen (the status bar) and from the About menu item.
func (a *App) showAbout() {
	details := a.aboutDetails()
	text := details.String()

	title := heading("vlt")
	title.Alignment = fyne.TextAlignCenter
	tagline := muted("A secret manager using age encryption.")
	tagline.Alignment = fyne.TextAlignCenter
	info := widget.NewLabel(strings.TrimSpace(text))
	info.TextStyle = fyne.TextStyle{Monospace: true}
	info.Wrapping = fyne.TextWrapBreak
	note := caption("The details hold no secrets, paths or names.")
	note.Alignment = fyne.TextAlignCenter

	var copyBtn *widget.Button
	copyBtn = widget.NewButtonWithIcon("Copy Details", theme.ContentCopyIcon(), func() {
		// A secret copied earlier is wiped and its countdown stopped, so the
		// status bar doesn't keep counting down for text that isn't there.
		a.clipboard.Clear()
		a.fyneApp.Clipboard().SetContent(text)
		copyBtn.SetText("Copied")
		copyBtn.SetIcon(theme.ConfirmIcon())
	})
	reportBtn := widget.NewButtonWithIcon("Report a Bug…", theme.MailComposeIcon(), func() {
		a.openURL(issueURL(text))
	})
	reportBtn.Importance = widget.HighImportance
	content := fixedWidth(container.NewVBox(
		container.NewCenter(brandMark(56)),
		title,
		tagline,
		card(info),
		note,
		container.NewCenter(container.NewHBox(copyBtn, reportBtn)),
	), 420)
	dialog.NewCustom("About vlt", "Close", content, a.window).Show()
}

// openURL opens a link in the browser, or reports why it cannot.
func (a *App) openURL(link string) {
	u, err := url.Parse(link)
	if err == nil {
		err = a.open(u)
	}
	if err != nil {
		a.log.Warn("cannot open link", "url", projectURL, "err", err)
		dialog.ShowError(fmt.Errorf("cannot open the browser: %w; the project is at %s", err, projectURL), a.window)
	}
}
