package ui

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/crypto"
	"github.com/heyjdp/vlt/internal/store"
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
	// pending is decoded file content waiting for the user to pick an export
	// path; dropPending wipes it if the dialog is dismissed by a lock.
	pending []byte
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
				Group:            matchGroup(groupEntry.Text, fo.groups()),
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
// before replacing an existing one. The store's refusal carries the secret
// already there, so asking needs no second read of the vault.
func (fo *FileOps) importSecret(secret *store.Secret, f importedFile) {
	background(fo.run,
		func(ctx context.Context) (none, error) { return none{}, fo.vaultStore.Create(ctx, secret) },
		func(_ none, err error) {
			var exists *store.ExistsError
			if errors.As(err, &exists) {
				fo.askReplace(secret, exists, f)
				return
			}
			if err != nil {
				dialog.ShowError(fmt.Errorf("failed to save file secret: %w", err), fo.window)
				return
			}
			fo.importDone(secret, f)
		})
}

// askReplace asks before overwriting the secret that holds an import's
// file. If that file can't be read, or is held by a differently named
// secret (same SecretID), replacing is refused and the user is told why.
func (fo *FileOps) askReplace(secret *store.Secret, exists *store.ExistsError, f importedFile) {
	existing := exists.Existing
	if existing == nil {
		dialog.ShowError(fmt.Errorf("cannot import %q: a file already stored under that name in group %q can't be read, so vlt leaves it alone; rename the file and import again (%w)",
			secret.Name, secret.Group, exists.ReadErr), fo.window)
		return
	}
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
	fo.dropPending()
	fo.pending = data
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
	fo.pending = nil // the dialog completed; data is handled (or wiped) below
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
			n, err := fo.writePrivateExport(writer, data)
			if err != nil {
				if delErr := storage.Delete(writer.URI()); delErr != nil && !errors.Is(delErr, fs.ErrNotExist) {
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

// dropPending wipes decoded export content whose save dialog never
// completed (for example because a lock dismissed it).
func (fo *FileOps) dropPending() {
	if fo.pending != nil {
		crypto.Wipe(fo.pending)
		fo.pending = nil
	}
}

// writePrivateExport writes data to the file w names and wipes data. A
// local file on Unix is written so only the user can read it: the save
// dialog has already created it with the user's umask (often readable by
// everyone), and anyone who opened it since could read through that
// handle. So data goes to a new 0600 temp file in the same folder, which is
// renamed over the file; a planted file or symlink is replaced, never
// followed, and a failed export removes only the temp file. If the user may
// not add files to the folder, the existing file is made private and
// written in place, or not at all if it can't be (it may be another
// user's). Windows has no Unix modes (the folder's ACL applies), and other
// schemes are written as given.
func (fo *FileOps) writePrivateExport(w fyne.URIWriteCloser, data []byte) (int, error) {
	if w.URI().Scheme() != "file" || runtime.GOOS == "windows" {
		return writeExport(w, data)
	}
	path := w.URI().Path()
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".vlt-export-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			err = os.Chmod(path, 0o600)
			if err == nil {
				fo.log.Info("export written in place: the folder takes no new files")
				return writeExport(w, data)
			}
		}
		crypto.Wipe(data)
		_ = w.Close()
		return 0, fmt.Errorf("write privately: %w", err)
	}
	if err := w.Close(); err != nil {
		crypto.Wipe(data)
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return 0, fmt.Errorf("close: %w", err)
	}
	n, err := writeExport(syncOnClose{tmp}, data)
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return 0, err
	}
	syncFolder(dir)
	return n, nil
}

// syncFolder flushes a folder's entries, so a rename into it survives a
// crash. It is best effort: an export already reported as written is not
// failed afterwards.
func syncFolder(dir string) {
	d, err := os.Open(dir) //nolint:gosec // the folder the user picked in the save dialog
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// syncOnClose flushes a file to disk before closing it, so a renamed export
// is complete even after a crash.
type syncOnClose struct{ *os.File }

// Close syncs and closes the file; it closes it even if the sync fails.
func (f syncOnClose) Close() error {
	if err := f.Sync(); err != nil {
		_ = f.File.Close()
		return fmt.Errorf("sync: %w", err)
	}
	return f.File.Close()
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
