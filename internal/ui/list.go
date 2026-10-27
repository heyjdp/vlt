package ui

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
)

// SecretList displays a filterable list of secrets with category headers.
// Its methods must be called on the Fyne main goroutine; the vault is read
// in the background (see Refresh).
type SecretList struct {
	window     fyne.Window
	store      *store.Store
	detailView *DetailView
	log        *slog.Logger
	run        runner

	searchEntry *widget.Entry
	list        *widget.List
	content     *fyne.Container

	secrets  []*store.Secret
	filtered []*store.Secret

	// loading is true while a Refresh is in flight; replay holds the saves
	// and deletes made meanwhile, re-applied to the loaded result.
	loading bool
	replay  []func()

	// selected is the secret highlighted in the list, kept so filtering can
	// move or clear the highlight when rows shift; reselecting suppresses
	// OnSelected while the highlight is moved programmatically.
	selected    *store.Secret
	reselecting bool
}

// NewSecretList creates a new secret browser panel.
func NewSecretList(window fyne.Window, s *store.Store, detail *DetailView, log *slog.Logger, run runner) *SecretList {
	sl := &SecretList{
		window:     window,
		store:      s,
		detailView: detail,
		log:        log,
		run:        run,
	}

	sl.searchEntry = widget.NewEntry()
	sl.searchEntry.SetPlaceHolder("Search secrets...")
	sl.searchEntry.OnChanged = func(query string) {
		sl.filter(query)
	}

	sl.list = widget.NewList(
		func() int {
			return len(sl.filtered)
		},
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewLabel("group"),
				widget.NewLabel("Secret Name"),
				widget.NewLabel("type"),
			)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(sl.filtered) {
				return
			}
			sec := sl.filtered[id]
			items := obj.(*fyne.Container).Objects
			items[0].(*widget.Label).SetText(sec.Group)
			items[1].(*widget.Label).SetText(sec.Name)
			items[2].(*widget.Label).SetText(string(sec.Type))
		},
	)

	sl.list.OnSelected = func(id widget.ListItemID) {
		if sl.reselecting || id >= len(sl.filtered) {
			return
		}
		sec := sl.filtered[id]
		sl.selected = sec
		sl.detailView.ShowSecret(sec)
	}
	sl.list.OnUnselected = func(widget.ListItemID) {
		if !sl.reselecting {
			sl.selected = nil
		}
	}

	sl.content = container.NewBorder(
		sl.searchEntry,
		nil,
		nil,
		nil,
		sl.list,
	)

	return sl
}

// Content returns the list panel widget.
func (sl *SecretList) Content() *fyne.Container {
	return sl.content
}

// Refresh reloads all secrets from the store in the background, using the
// runner's session context (Lock cancels it), and updates the list when
// done. Saves and deletes applied while the load is in flight are replayed
// onto its result, because the load may have read the vault before them.
func (sl *SecretList) Refresh() {
	sl.loading = true
	sl.replay = nil
	background(sl.run,
		func(ctx context.Context) ([]*store.Secret, error) { return sl.store.List(ctx) },
		func(secrets []*store.Secret, err error) {
			replay := sl.replay
			sl.loading, sl.replay = false, nil
			if err != nil {
				sl.log.Error("failed to list secrets", "err", err)
				return
			}
			sl.secrets = secrets
			for _, op := range replay {
				op()
			}
			sl.filter(sl.searchEntry.Text)
		})
}

// Upsert adds a saved secret to the list, or replaces the entry stored at
// the same path, without re-reading the vault.
func (sl *SecretList) Upsert(secret *store.Secret) {
	sl.upsert(secret)
	if sl.loading {
		sl.replay = append(sl.replay, func() { sl.upsert(secret) })
	}
	sl.filter(sl.searchEntry.Text)
}

func (sl *SecretList) upsert(secret *store.Secret) {
	for i, s := range sl.secrets {
		if sameSecret(s, secret) {
			sl.secrets[i] = secret
			return
		}
	}
	sl.secrets = append(sl.secrets, secret)
}

// Remove drops a deleted secret from the list; the highlight is cleared if
// it was on that secret.
func (sl *SecretList) Remove(secret *store.Secret) {
	sl.remove(secret)
	if sl.loading {
		sl.replay = append(sl.replay, func() { sl.remove(secret) })
	}
	sl.filter(sl.searchEntry.Text) // clears the highlight if it was on this secret
}

func (sl *SecretList) remove(secret *store.Secret) {
	kept := make([]*store.Secret, 0, len(sl.secrets))
	for _, s := range sl.secrets {
		if !sameSecret(s, secret) {
			kept = append(kept, s)
		}
	}
	sl.secrets = kept
}

// Groups returns the sorted, distinct groups of the loaded secrets, one
// label per group directory (the first one loaded, if spellings differ;
// secrets saved before the first load completes can add a second spelling
// to a directory). It
// is used for group suggestions so the UI never reads the vault directory on
// the main goroutine. It is empty until the first load completes and, unlike
// the directory listing, omits empty group directories.
func (sl *SecretList) Groups() []string {
	seen := make(map[string]bool)
	var groups []string
	for _, s := range sl.secrets {
		dir := store.NormalizeGroup(s.Group)
		if !seen[dir] {
			seen[dir] = true
			groups = append(groups, s.Group)
		}
	}
	slices.SortFunc(groups, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	return groups
}

// matchGroup returns the existing group that shares typed's directory, so
// typing "work" in a vault with a "Work" group files the secret under "Work".
// Otherwise it returns typed unchanged.
func matchGroup(typed string, groups []string) string {
	dir := store.NormalizeGroup(typed)
	for _, g := range groups {
		if store.NormalizeGroup(g) == dir {
			return g
		}
	}
	return typed
}

// sameSecret reports whether a and b are stored at the same path.
func sameSecret(a, b *store.Secret) bool {
	return store.NormalizeGroup(a.Group) == store.NormalizeGroup(b.Group) &&
		store.Slugify(a.Name) == store.Slugify(b.Name)
}

// filter applies the search query to the secret list.
func (sl *SecretList) filter(query string) {
	query = strings.ToLower(strings.TrimSpace(query))

	// The group also matches by directory, so "personal-finance" finds
	// "Personal Finance" and the reverse.
	slug := store.Slugify(query)

	if query == "" {
		sl.filtered = sl.secrets
	} else {
		sl.filtered = nil
		for _, sec := range sl.secrets {
			if strings.Contains(strings.ToLower(sec.Name), query) {
				sl.filtered = append(sl.filtered, sec)
				continue
			}
			if strings.Contains(strings.ToLower(sec.Group), query) ||
				(slug != "" && strings.Contains(store.NormalizeGroup(sec.Group), slug)) {
				sl.filtered = append(sl.filtered, sec)
				continue
			}
			for _, tag := range sec.Tags {
				if strings.Contains(strings.ToLower(tag), query) {
					sl.filtered = append(sl.filtered, sec)
					break
				}
			}
		}
	}

	sl.list.Refresh()
	sl.syncSelection()
}

// syncSelection keeps the list highlight on the selected secret after the
// rows change: it moves the highlight if the secret's row moved, and clears
// it if the secret is no longer shown (deleted, or filtered out).
func (sl *SecretList) syncSelection() {
	if sl.selected == nil {
		return
	}
	for i, s := range sl.filtered {
		if sameSecret(s, sl.selected) {
			sl.reselecting = true
			sl.list.Select(i)
			sl.reselecting = false
			return
		}
	}
	sl.selected = nil
	sl.reselecting = true
	sl.list.UnselectAll()
	sl.reselecting = false
}

// clear drops every loaded secret (used when the vault locks).
func (sl *SecretList) clear() {
	for _, s := range sl.secrets {
		*s = store.Secret{} // empty the struct itself, not just the slice
	}
	sl.secrets, sl.filtered, sl.replay, sl.selected = nil, nil, nil, nil
	sl.searchEntry.OnChanged = nil // clearing the search is not user activity
	sl.searchEntry.SetText("")
	sl.list.OnSelected, sl.list.OnUnselected = nil, nil
	sl.list.Refresh()
}
