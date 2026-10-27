package ui

import (
	"context"
	"log/slog"
	"sort"
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
		if id >= len(sl.filtered) {
			return
		}
		sec := sl.filtered[id]
		sl.detailView.ShowSecret(sec)
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

// Upsert adds a saved secret to the list, or replaces the entry with the
// same name and group, without re-reading the vault.
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

// Remove drops a deleted secret from the list and clears the selection.
func (sl *SecretList) Remove(secret *store.Secret) {
	sl.remove(secret)
	if sl.loading {
		sl.replay = append(sl.replay, func() { sl.remove(secret) })
	}
	sl.list.UnselectAll()
	sl.filter(sl.searchEntry.Text)
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

// Groups returns the sorted, distinct groups of the loaded secrets. It is
// used for group suggestions so the UI never reads the vault directory on
// the main goroutine. It is empty until the first load completes and, unlike
// the directory listing, omits empty group directories.
func (sl *SecretList) Groups() []string {
	seen := make(map[string]bool)
	var groups []string
	for _, s := range sl.secrets {
		if !seen[s.Group] {
			seen[s.Group] = true
			groups = append(groups, s.Group)
		}
	}
	sort.Strings(groups)
	return groups
}

// sameSecret reports whether a and b are stored at the same path.
func sameSecret(a, b *store.Secret) bool {
	return store.NormalizeGroup(a.Group) == store.NormalizeGroup(b.Group) &&
		store.Slugify(a.Name) == store.Slugify(b.Name)
}

// filter applies the search query to the secret list.
func (sl *SecretList) filter(query string) {
	query = strings.ToLower(strings.TrimSpace(query))

	if query == "" {
		sl.filtered = sl.secrets
	} else {
		sl.filtered = nil
		for _, sec := range sl.secrets {
			if strings.Contains(strings.ToLower(sec.Name), query) {
				sl.filtered = append(sl.filtered, sec)
				continue
			}
			if strings.Contains(strings.ToLower(sec.Group), query) {
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
}
