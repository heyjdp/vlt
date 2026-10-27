package ui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/store"
)

const maxFileSize = 10 * 1024 * 1024 // 10 MB

// FileOps handles file import and export operations. Store calls run in the
// background.
type FileOps struct {
	window     fyne.Window
	vaultStore *store.Store
	log        *slog.Logger
	run        runner
	groups     func() []string
	onSaved    func(*store.Secret)
	// activity reports user input in the file and form dialogs, which are
	// outside the instrumented widget tree.
	activity func()
}

// NewFileOps creates a file operations handler. groups supplies group
// suggestions and onSaved is called with each imported secret.
func NewFileOps(window fyne.Window, s *store.Store, log *slog.Logger, run runner, groups func() []string, onSaved func(*store.Secret)) *FileOps {
	return &FileOps{
		window:     window,
		vaultStore: s,
		log:        log,
		run:        run,
		groups:     groups,
		onSaved:    onSaved,
		activity:   noActivity,
	}
}

// ImportFile opens a file picker and imports the selected file as a file secret.
func (fo *FileOps) ImportFile() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		fo.activity()
		if err != nil {
			dialog.ShowError(err, fo.window)
			return
		}
		if reader == nil {
			return // user cancelled
		}
		defer reader.Close()

		fo.processImport(reader)
	}, fo.window)
	fd.Show()
}

// processImport reads the file, validates size, detects MIME type, and saves.
func (fo *FileOps) processImport(reader fyne.URIReadCloser) {
	uri := reader.URI()
	filename := filepath.Base(uri.Name())

	// Read file content
	data := make([]byte, 0, 4096)
	buf := make([]byte, 32*1024)
	totalRead := 0

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			totalRead += n
			if totalRead > maxFileSize {
				dialog.ShowError(fmt.Errorf("file exceeds maximum size of 10 MB"), fo.window)
				return
			}
			data = append(data, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	// Detect MIME type
	mimeType := http.DetectContentType(data)

	// Base64 encode
	encoded := base64.StdEncoding.EncodeToString(data)

	// Ask for group before saving
	groupEntry := widget.NewSelectEntry(fo.groups())
	groupEntry.OnChanged = func(string) { fo.activity() }
	groupEntry.SetPlaceHolder("Group (e.g. work, personal)")

	dialog.ShowForm("Import File — Select Group",
		"Import", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Group", groupEntry),
		},
		func(confirmed bool) {
			fo.activity()
			if !confirmed {
				return
			}

			now := time.Now().UTC()
			secret := &store.Secret{
				Version:          1,
				Type:             store.TypeFile,
				Name:             filename,
				Group:            groupEntry.Text,
				CreatedAt:        now,
				ModifiedAt:       now,
				OriginalFilename: filename,
				MIMEType:         mimeType,
				Encoding:         "base64",
				Value:            encoded,
			}

			fo.importSecret(secret, totalRead)
		},
		fo.window,
	)
}

// importSecret creates an imported file secret in the background, asking
// before replacing an existing one.
func (fo *FileOps) importSecret(secret *store.Secret, size int) {
	background(fo.run,
		func(ctx context.Context) (none, error) { return none{}, fo.vaultStore.Create(ctx, secret) },
		func(_ none, err error) {
			if errors.Is(err, store.ErrSecretExists) {
				fo.confirmReplace(secret, size)
				return
			}
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to save file secret: %w", err), fo.window)
				return
			}
			fo.importDone(secret, size)
		})
}

// confirmReplace asks before overwriting an existing secret with an import.
// If the file is held by a differently named secret (same slug), replacing
// is impossible and the user is told to rename instead.
func (fo *FileOps) confirmReplace(secret *store.Secret, size int) {
	background(fo.run,
		func(ctx context.Context) (*store.Secret, error) {
			return fo.vaultStore.Get(ctx, secret.Name, secret.Group)
		},
		func(existing *store.Secret, err error) {
			if err != nil {
				dialog.ShowError(fmt.Errorf("import failed: %w", err), fo.window)
				return
			}
			fo.askReplace(secret, existing, size)
		})
}

// askReplace shows the replace confirmation for an import whose name is
// already taken by existing.
func (fo *FileOps) askReplace(secret, existing *store.Secret, size int) {
	if existing.Name != secret.Name {
		dialog.ShowError(fmt.Errorf("cannot import %q: its filename is already used by %q in group %q; rename the file and import again",
			secret.Name, existing.Name, secret.Group), fo.window)
		return
	}
	dialog.ShowConfirm("Secret already exists",
		fmt.Sprintf("A %s secret named %q already exists in group %q.\nReplace it with the imported file?", existing.Type, secret.Name, secret.Group),
		func(replace bool) {
			fo.activity()
			if !replace {
				return
			}
			background(fo.run,
				func(ctx context.Context) (none, error) { return none{}, fo.vaultStore.Update(ctx, secret) },
				func(_ none, err error) {
					if err != nil {
						dialog.ShowError(fmt.Errorf("replace failed: %w", err), fo.window)
						return
					}
					fo.importDone(secret, size)
				})
		},
		fo.window,
	)
}

// importDone logs and reports a successful import and refreshes the list.
func (fo *FileOps) importDone(secret *store.Secret, size int) {
	fo.log.Info("file imported", "name", secret.Name, "group", secret.Group, "size", size, "mime", secret.MIMEType)
	dialog.ShowInformation("Import Complete",
		fmt.Sprintf("Imported %q to group %q (%s, %d bytes)", secret.Name, secret.Group, secret.MIMEType, size),
		fo.window,
	)
	if fo.onSaved != nil {
		fo.onSaved(secret)
	}
}

// ExportFile decodes a file secret and saves it to a user-selected location.
func (fo *FileOps) ExportFile(secret *store.Secret) {
	if secret.Type != store.TypeFile {
		dialog.ShowError(fmt.Errorf("not a file secret"), fo.window)
		return
	}

	// Decode base64 content
	data, err := base64.StdEncoding.DecodeString(secret.Value)
	if err != nil {
		dialog.ShowError(fmt.Errorf("failed to decode file content: %w", err), fo.window)
		return
	}

	fd := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		fo.activity()
		if err != nil {
			dialog.ShowError(err, fo.window)
			return
		}
		if writer == nil {
			return // user cancelled
		}
		defer writer.Close()

		if _, err := writer.Write(data); err != nil {
			dialog.ShowError(fmt.Errorf("failed to write file: %w", err), fo.window)
			return
		}

		fo.log.Info("file exported", "name", secret.Name, "size", len(data))
		dialog.ShowInformation("Export Complete",
			fmt.Sprintf("Exported %q (%d bytes)", secret.OriginalFilename, len(data)),
			fo.window,
		)
	}, fo.window)

	// Suggest the original filename
	fd.SetFileName(secret.OriginalFilename)

	// Try to set a filter based on MIME type extension
	if ext := filepath.Ext(secret.OriginalFilename); ext != "" {
		fd.SetFilter(storage.NewExtensionFileFilter([]string{ext}))
	}

	fd.Show()
}
