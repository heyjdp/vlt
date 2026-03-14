package ui

import (
	"context"
	"log/slog"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
)

// SecretList displays a filterable list of secrets with category headers.
type SecretList struct {
	window     fyne.Window
	store      *store.Store
	detailView *DetailView
	log        *slog.Logger

	searchEntry *widget.Entry
	list        *widget.List
	content     *fyne.Container

	secrets  []*store.Secret
	filtered []*store.Secret
}

// NewSecretList creates a new secret browser panel.
func NewSecretList(window fyne.Window, s *store.Store, detail *DetailView, log *slog.Logger) *SecretList {
	sl := &SecretList{
		window:     window,
		store:      s,
		detailView: detail,
		log:        log,
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
				widget.NewIcon(nil),
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

// Refresh reloads all secrets from the store.
func (sl *SecretList) Refresh(ctx context.Context) {
	secrets, err := sl.store.List(ctx)
	if err != nil {
		sl.log.Error("failed to list secrets", "err", err)
		return
	}

	fyne.Do(func() {
		sl.secrets = secrets
		sl.filter(sl.searchEntry.Text)
	})
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
