package ui

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// failingReader yields n bytes, then fails.
func failingReader(n int) io.Reader {
	return io.MultiReader(bytes.NewReader(make([]byte, n)), iotest.ErrReader(errors.New("disk went away")))
}

func TestReadImport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		r       io.Reader
		wantLen int
		wantErr bool
	}{
		{"empty file", strings.NewReader(""), 0, false},
		{"exactly the limit", bytes.NewReader(make([]byte, maxFileSize)), maxFileSize, false},
		{"one byte over", bytes.NewReader(make([]byte, maxFileSize+1)), 0, true},
		{"read error part-way", failingReader(1000), 0, true},
		{"read error at once", iotest.ErrReader(errors.New("boom")), 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data, err := readImport(tt.r)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %d bytes", len(data))
				}
				return
			}
			if err != nil || len(data) != tt.wantLen {
				t.Fatalf("got %d bytes, err %v; want %d bytes", len(data), err, tt.wantLen)
			}
		})
	}
}

func TestReadImport_OverLimitIsErrFileTooLarge(t *testing.T) {
	t.Parallel()
	if _, err := readImport(bytes.NewReader(make([]byte, maxFileSize+1))); !errors.Is(err, errFileTooLarge) {
		t.Fatalf("expected errFileTooLarge, got %v", err)
	}
}

// fakeURIReader is a fyne.URIReadCloser over an io.Reader.
type fakeURIReader struct {
	io.Reader
	uri    fyne.URI
	closed bool
}

func (f *fakeURIReader) URI() fyne.URI { return f.uri }
func (f *fakeURIReader) Close() error  { f.closed = true; return nil }

func TestPrepareImport_ReadErrorClosesAndFails(t *testing.T) {
	t.Parallel()
	r := &fakeURIReader{Reader: failingReader(4096), uri: storage.NewFileURI("/tmp/id_rsa")}

	if _, err := prepareImport(r); err == nil {
		t.Fatal("a read error must fail the import")
	}
	if !r.closed {
		t.Fatal("the file must be closed even when reading fails")
	}
}

func TestPrepareImport_EncodesAndDigests(t *testing.T) {
	t.Parallel()
	r := &fakeURIReader{Reader: strings.NewReader("abc"), uri: storage.NewFileURI("/tmp/notes.txt")}

	f, err := prepareImport(r)
	if err != nil {
		t.Fatal(err)
	}
	if f.name != "notes.txt" || f.encoded != "YWJj" || f.size != 3 {
		t.Fatalf("unexpected import: %+v", f)
	}
	if f.digest != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("unexpected digest %s", f.digest)
	}
}

func TestApp_FailingImportSavesNothing(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	r := &fakeURIReader{Reader: failingReader(4096), uri: storage.NewFileURI("/tmp/id_rsa")}
	app.detailView.fileOps.processImport(r)

	secrets, err := app.store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 || len(app.secretList.secrets) != 0 {
		t.Fatal("a failed read must not save anything")
	}
	app.Lock()
}

// fakeWriter is an io.WriteCloser whose Close can fail.
type fakeWriter struct {
	bytes.Buffer
	closeErr error
}

func (w *fakeWriter) Close() error { return w.closeErr }

func TestWriteExport(t *testing.T) {
	t.Parallel()

	ok := &fakeWriter{}
	data := []byte("secret file")
	n, err := writeExport(ok, data)
	if err != nil || n != 11 || ok.String() != "secret file" {
		t.Fatalf("writeExport = %d, %v; wrote %q", n, err, ok.String())
	}
	if !bytes.Equal(data, make([]byte, 11)) {
		t.Fatal("exported plaintext should be wiped after writing")
	}

	failing := &fakeWriter{closeErr: errors.New("disk full")}
	if _, err := writeExport(failing, []byte("x")); err == nil {
		t.Fatal("a failed close must be reported")
	}
}

func TestApp_FailingImportShowsErrorNotGroupForm(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	app.detailView.fileOps.processImport(&fakeURIReader{Reader: failingReader(10), uri: storage.NewFileURI("/tmp/x")})

	top := w.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("a failed import should show an error dialog")
	}
	if findType[*widget.SelectEntry](top) != nil {
		t.Fatal("a failed import must not ask for a group")
	}
	app.Lock()
}

// closeFailingReader reads fine but fails to close.
type closeFailingReader struct{ fakeURIReader }

func (r *closeFailingReader) Close() error { return errors.New("close failed") }

func TestPrepareImport_CloseErrorFails(t *testing.T) {
	t.Parallel()
	r := &closeFailingReader{fakeURIReader{Reader: strings.NewReader("abc"), uri: storage.NewFileURI("/tmp/x")}}
	if _, err := prepareImport(r); err == nil {
		t.Fatal("a failed close must fail the import")
	}
}

// writeFailingWriter fails every write.
type writeFailingWriter struct{ fakeWriter }

func (w *writeFailingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteExport_WriteErrorIsReportedAndWipes(t *testing.T) {
	t.Parallel()
	data := []byte("secret")
	if _, err := writeExport(&writeFailingWriter{}, data); err == nil {
		t.Fatal("a failed write must be reported")
	}
	if !bytes.Equal(data, make([]byte, 6)) {
		t.Fatal("plaintext must be wiped even when the write fails")
	}
}

// fakeURIWriter records whether anything was written.
type fakeURIWriter struct {
	fakeWriter
	uri fyne.URI
}

func (w *fakeURIWriter) URI() fyne.URI { return w.uri }

func TestApp_ExportAfterLockWritesNothing(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	app := newApp(a, test.NewTempWindow(t, nil), Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)
	fo := app.detailView.fileOps
	app.Lock() // the save dialog was still open when the vault locked

	data := []byte("plaintext")
	w := &fakeURIWriter{uri: storage.NewFileURI(filepath.Join(t.TempDir(), "out"))}
	fo.saveExport(w, nil, "n", "out", data)

	if w.Len() != 0 {
		t.Fatal("an export confirmed after lock must not write plaintext")
	}
	if !bytes.Equal(data, make([]byte, len(data))) {
		t.Fatal("the export buffer must be wiped")
	}
}

func TestApp_LockDismissesOpenDialogs(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, Config{VaultDir: dir}, quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	dialog.ShowInformation("open", "a dialog left open", w)
	app.Lock()

	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("locking must dismiss dialogs left open in the session")
	}
}
