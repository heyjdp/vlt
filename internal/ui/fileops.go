package ui

import (
	"context"
	"encoding/base64"
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

// FileOps handles file import and export operations.
type FileOps struct {
	window    fyne.Window
	vaultStore *store.Store
	log       *slog.Logger
	onSave    func()
}

// NewFileOps creates a file operations handler.
func NewFileOps(window fyne.Window, s *store.Store, log *slog.Logger) *FileOps {
	return &FileOps{
		window:    window,
		vaultStore: s,
		log:       log,
	}
}

// SetOnSave sets a callback invoked after a successful import.
func (fo *FileOps) SetOnSave(fn func()) {
	fo.onSave = fn
}

// ImportFile opens a file picker and imports the selected file as a file secret.
func (fo *FileOps) ImportFile() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
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
	groups := fo.vaultStore.Groups()
	groupEntry := widget.NewSelectEntry(groups)
	groupEntry.SetPlaceHolder("Group (e.g. work, personal)")

	dialog.ShowForm("Import File — Select Group",
		"Import", "Cancel",
		[]*widget.FormItem{
			widget.NewFormItem("Group", groupEntry),
		},
		func(confirmed bool) {
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

			ctx := context.Background()
			if err := fo.vaultStore.Save(ctx, secret); err != nil {
				dialog.ShowError(fmt.Errorf("failed to save file secret: %w", err), fo.window)
				return
			}

			fo.log.Info("file imported", "name", filename, "group", secret.Group, "size", totalRead, "mime", mimeType)

			dialog.ShowInformation("Import Complete",
				fmt.Sprintf("Imported %q to group %q (%s, %d bytes)", filename, secret.Group, mimeType, totalRead),
				fo.window,
			)

			if fo.onSave != nil {
				fo.onSave()
			}
		},
		fo.window,
	)
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
