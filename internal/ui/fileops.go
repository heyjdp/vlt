package ui

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/yourorg/vlt/internal/crypto"
	"github.com/yourorg/vlt/internal/store"
)

// maxFileSize is the largest file that can be imported: 10 MiB.
const maxFileSize = 10 * 1024 * 1024

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

// errFileTooLarge is returned for imports over maxFileSize.
var errFileTooLarge = errors.New("file exceeds the maximum size of 10 MiB")

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
		fo.processImport(reader)
	}, fo.window)
	fd.Show()
}

// importedFile is a file read for import, ready to become a file secret.
type importedFile struct {
	name    string
	mime    string
	encoded string // base64
	digest  string // hex SHA-256 of the file's bytes, shown to the user
	size    int
}

// readImport reads all of r, up to maxFileSize bytes. Any read error is
// returned rather than treated as end of file, so a failure part-way
// through can never import a truncated file.
// Bytes already read are wiped before an error is returned.
func readImport(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxFileSize+1))
	if err != nil {
		crypto.Wipe(data)
		return nil, err
	}
	if len(data) > maxFileSize {
		crypto.Wipe(data)
		return nil, errFileTooLarge
	}
	return data, nil
}

// prepareImport reads and closes the file behind reader, detects its MIME
// type, base64-encodes it and computes its SHA-256, then wipes the raw bytes.
// It runs in the background: a 10 MiB read plus encoding must not block the
// UI.
func prepareImport(reader fyne.URIReadCloser) (importedFile, error) {
	name := filepath.Base(reader.URI().Name())
	data, err := readImport(reader)
	closeErr := reader.Close()
	if err != nil {
		return importedFile{}, fmt.Errorf("read %q: %w", name, err)
	}
	if closeErr != nil {
		crypto.Wipe(data)
		return importedFile{}, fmt.Errorf("close %q: %w", name, closeErr)
	}

	sum := sha256.Sum256(data)
	f := importedFile{
		name:    name,
		mime:    http.DetectContentType(data),
		encoded: base64.StdEncoding.EncodeToString(data),
		digest:  hex.EncodeToString(sum[:]),
		size:    len(data),
	}
	crypto.Wipe(data)
	return f, nil
}

// processImport reads the file in the background, then asks for a group and
// saves it. A read error or an oversized file is reported and nothing is
// saved.
func (fo *FileOps) processImport(reader fyne.URIReadCloser) {
	background(fo.run,
		func(context.Context) (importedFile, error) { return prepareImport(reader) },
		func(f importedFile, err error) {
			if err != nil {
				dialog.ShowError(fmt.Errorf("import failed: %w", err), fo.window)
				return
			}
			fo.askGroup(f)
		})
}

// askGroup asks which group an imported file belongs to, then saves it.
func (fo *FileOps) askGroup(f importedFile) {
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
				Name:             f.name,
				Group:            groupEntry.Text,
				CreatedAt:        now,
				ModifiedAt:       now,
				OriginalFilename: f.name,
				MIMEType:         f.mime,
				Encoding:         "base64",
				Value:            f.encoded,
			}
			fo.importSecret(secret, f)
		},
		fo.window,
	)
}

// importSecret creates an imported file secret in the background, asking
// before replacing an existing one.
func (fo *FileOps) importSecret(secret *store.Secret, f importedFile) {
	background(fo.run,
		func(ctx context.Context) (none, error) { return none{}, fo.vaultStore.Create(ctx, secret) },
		func(_ none, err error) {
			if errors.Is(err, store.ErrSecretExists) {
				fo.confirmReplace(secret, f)
				return
			}
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to save file secret: %w", err), fo.window)
				return
			}
			fo.importDone(secret, f)
		})
}

// confirmReplace asks before overwriting an existing secret with an import.
// If the file is held by a differently named secret (same slug), replacing
// is impossible and the user is told to rename instead.
func (fo *FileOps) confirmReplace(secret *store.Secret, f importedFile) {
	background(fo.run,
		func(ctx context.Context) (*store.Secret, error) {
			return fo.vaultStore.Get(ctx, secret.Name, secret.Group)
		},
		func(existing *store.Secret, err error) {
			if err != nil {
				dialog.ShowError(fmt.Errorf("import failed: %w", err), fo.window)
				return
			}
			fo.askReplace(secret, existing, f)
		})
}

// askReplace shows the replace confirmation for an import whose name is
// already taken by existing.
func (fo *FileOps) askReplace(secret, existing *store.Secret, f importedFile) {
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
					fo.importDone(secret, f)
				})
		},
		fo.window,
	)
}

// importDone logs and reports a successful import, including the file's
// SHA-256 so the user can check it, and updates the list.
func (fo *FileOps) importDone(secret *store.Secret, f importedFile) {
	fo.log.Info("file imported", "name", secret.Name, "group", secret.Group, "size", f.size, "mime", secret.MIMEType)
	dialog.ShowInformation("Import Complete",
		fmt.Sprintf("Imported %q to group %q (%s, %d bytes)\nSHA-256: %s", secret.Name, secret.Group, secret.MIMEType, f.size, f.digest),
		fo.window,
	)
	if fo.onSaved != nil {
		fo.onSaved(secret)
	}
}

// ExportFile decodes a file secret in the background, then asks where to
// save it and writes it in the background.
func (fo *FileOps) ExportFile(secret *store.Secret) {
	if secret.Type != store.TypeFile {
		dialog.ShowError(fmt.Errorf("not a file secret"), fo.window)
		return
	}
	// Copy what the background work needs: the list's *Secret may be
	// rewritten by a save on the main goroutine meanwhile.
	encoded, name, filename := secret.Value, secret.Name, secret.OriginalFilename

	background(fo.run,
		func(ctx context.Context) ([]byte, error) {
			data, err := base64.StdEncoding.DecodeString(encoded)
			if err == nil && ctx.Err() != nil {
				// The vault locked meanwhile; the result will be dropped.
				crypto.Wipe(data)
				return nil, ctx.Err()
			}
			return data, err
		},
		func(data []byte, err error) {
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to decode file content: %w", err), fo.window)
				return
			}
			fo.askExportPath(name, filename, data)
		})
}

// askExportPath shows the save dialog and writes data to the chosen file.
func (fo *FileOps) askExportPath(name, filename string, data []byte) {
	fd := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		fo.saveExport(writer, err, name, filename, data)
	}, fo.window)

	fd.SetFileName(filename)
	if ext := filepath.Ext(filename); ext != "" {
		fd.SetFilter(storage.NewExtensionFileFilter([]string{ext}))
	}
	fd.Show()
}

// saveExport handles the save dialog's result: it writes data to writer in
// the background and reports the outcome. If the vault locked while the
// dialog was open, nothing is written. A failed write removes the partial
// file where possible, because the destination was already truncated.
func (fo *FileOps) saveExport(writer fyne.URIWriteCloser, err error, name, filename string, data []byte) {
	fo.activity()
	if fo.run.context().Err() != nil {
		crypto.Wipe(data)
		if writer != nil {
			_ = writer.Close()
		}
		return // the session ended; never write plaintext after a lock
	}
	if err != nil {
		crypto.Wipe(data)
		dialog.ShowError(err, fo.window)
		return
	}
	if writer == nil {
		crypto.Wipe(data)
		return // user cancelled
	}
	background(fo.run,
		func(context.Context) (int, error) {
			n, err := writeExport(writer, data)
			if err != nil {
				if delErr := storage.Delete(writer.URI()); delErr != nil {
					return 0, fmt.Errorf("%w (the file at %s may be incomplete)", err, writer.URI().Path())
				}
			}
			return n, err
		},
		func(n int, err error) {
			if err != nil {
				dialog.ShowError(fmt.Errorf("export failed: %w", err), fo.window)
				return
			}
			fo.log.Info("file exported", "name", name, "size", n)
			dialog.ShowInformation("Export Complete",
				fmt.Sprintf("Exported %q (%d bytes)", filename, n),
				fo.window,
			)
		})
}

// writeExport writes data to w and closes it, then wipes data. A failed
// close is reported, since it can mean the data never reached the disk.
func writeExport(w io.WriteCloser, data []byte) (int, error) {
	defer crypto.Wipe(data)
	_, writeErr := w.Write(data)
	closeErr := w.Close()
	if writeErr != nil {
		return 0, fmt.Errorf("write: %w", writeErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close: %w", closeErr)
	}
	return len(data), nil
}
