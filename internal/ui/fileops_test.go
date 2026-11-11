package ui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/testutil"
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
	app := newApp(a, test.NewTempWindow(t, nil), settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	r := &fakeURIReader{Reader: failingReader(4096), uri: storage.NewFileURI("/tmp/id_rsa")}
	importFile(t, app, r)

	secrets, err := app.session.Store().Contents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets.Secrets) != 0 || len(app.secretList.secrets) != 0 {
		t.Fatal("a failed read must not save anything")
	}
	app.Lock()
}

// fakeWriter is an io.WriteCloser whose Close can fail. It keeps the
// slices it was given (not copies), so a test can see them wiped.
type fakeWriter struct {
	bytes.Buffer
	closeErr error
	closed   bool
	given    [][]byte
}

func (w *fakeWriter) Write(p []byte) (int, error) {
	w.given = append(w.given, p)
	return w.Buffer.Write(p)
}

func (w *fakeWriter) Close() error { w.closed = true; return w.closeErr }

func TestWriteExport(t *testing.T) {
	t.Parallel()

	ok := &fakeWriter{}
	data := []byte("secret file")
	n, err := writeExport(ok, data)
	if err != nil || n != 11 || ok.String() != "secret file" || !ok.closed {
		t.Fatalf("writeExport = %d, %v; wrote %q, closed %v", n, err, ok.String(), ok.closed)
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
	app := newApp(a, w, settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	importFile(t, app, &fakeURIReader{Reader: failingReader(10), uri: storage.NewFileURI("/tmp/x")})

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

// writeFailingWriter fails every write, after keeping what it was given.
type writeFailingWriter struct{ fakeWriter }

func (w *writeFailingWriter) Write(p []byte) (int, error) {
	w.given = append(w.given, p)
	return 0, errors.New("disk full")
}

func TestWriteExport_WriteErrorIsReported(t *testing.T) {
	t.Parallel()
	w := &writeFailingWriter{}
	if _, err := writeExport(w, []byte("secret")); err == nil || !w.closed {
		t.Fatalf("a failed write must be reported (got %v) and the file closed (%v)", err, w.closed)
	}
}

// fakeURIWriter is a file to export to that is not on the local disk (a
// "mem" URI), so the export writes it directly and a test sees the bytes.
type fakeURIWriter struct {
	fakeWriter
	uri fyne.URI
}

func (w *fakeURIWriter) URI() fyne.URI { return w.uri }

// memWriter returns a fakeURIWriter for a file named name.
func memWriter(t *testing.T, name string) *fakeURIWriter {
	t.Helper()
	uri, err := storage.ParseURI("mem:///" + name)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeURIWriter{uri: uri}
}

// exportApp is an unlocked app whose vault holds one file secret, "notes"
// (notes.txt, "hello"), with a scripted picker.
func exportApp(t *testing.T) (*App, fyne.Window, *scriptedPicker) {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.FileSecret("notes", "notes.txt", "aGVsbG8=", "docs"))
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	p := pickerOf(app)
	unlock(app)
	return app, w, p
}

// startExport shows the secret and taps Export File, which leaves the save
// dialog open.
func startExport(t *testing.T, app *App) {
	t.Helper()
	app.secretList.list.Select(0)
	test.Tap(findButton(app.detailView.Content(), "Export File"))
}

func TestApp_ExportAfterLockWritesNothing(t *testing.T) {
	app, _, p := exportApp(t)
	startExport(t, app)
	app.Lock() // the save dialog was still open when the vault locked

	w := memWriter(t, "notes.txt")
	p.answerSave(t, w, nil) // a path chosen too late

	if w.Len() != 0 || !w.closed {
		t.Fatalf("an export confirmed after lock must write nothing (wrote %d bytes) and close the file (%v)", w.Len(), w.closed)
	}
}

// A lock while the file is being decoded drops the export: no save dialog
// is asked for and nothing is shown.
func TestApp_LockDuringExportDecodeAsksNothing(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir, testutil.FileSecret("notes", "notes.txt", "aGVsbG8=", "docs"))
	w := test.NewTempWindow(t, nil)
	q := &queue{}
	app := newApp(a, w, settingsAt(a, dir), quietLog(), q.runner(), noTimers, testKDF)
	q.flush()
	unlockQueued(t, app, q)
	q.flush() // the list loads
	p := pickerOf(app)
	startExport(t, app)

	app.Lock() // while the decode is in flight
	q.flush()

	if len(p.saves) != 0 || w.Canvas().Overlays().Top() != nil {
		t.Fatalf("an export the lock overtook asked %d save dialogs and showed a dialog: %v", len(p.saves), w.Canvas().Overlays().Top() != nil)
	}
}

func TestApp_ExportWipesWhatItWrote(t *testing.T) {
	app, w, p := exportApp(t)
	startExport(t, app)

	out := memWriter(t, "notes.txt")
	if name := p.answerSave(t, out, nil); name != "notes.txt" {
		t.Fatalf("the save dialog suggested %q, want the original file name", name)
	}

	if out.String() != "hello" || !visibleText(w.Canvas().Overlays().Top(), "Exported \"notes.txt\" (5 bytes)") {
		t.Fatalf("export wrote %q", out.String())
	}
	for _, p := range out.given {
		if !bytes.Equal(p, make([]byte, len(p))) {
			t.Fatal("the decoded file must be wiped once it is written")
		}
	}
	app.Lock()
}

// The decoded file is wiped whichever way an export ends.
func TestFileOps_ExportWipesThePlaintextWhicheverWayItEnds(t *testing.T) {
	tests := []struct {
		name string
		end  func(t *testing.T, app *App, p *scriptedPicker)
	}{
		{"cancelled", func(t *testing.T, _ *App, p *scriptedPicker) { p.answerSave(t, nil, nil) }},
		{"the save dialog fails", func(t *testing.T, _ *App, p *scriptedPicker) {
			p.answerSave(t, nil, errors.New("no such folder"))
		}},
		{"locked with the dialog open", func(_ *testing.T, app *App, _ *scriptedPicker) { app.Lock() }},
		{"the write fails", func(t *testing.T, _ *App, p *scriptedPicker) {
			p.answerSave(t, &writeFailingURIWriter{fakeURIWriter: *memWriter(t, "out.txt")}, nil)
		}},
		{"written", func(t *testing.T, _ *App, p *scriptedPicker) { p.answerSave(t, memWriter(t, "out.txt"), nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, _, p := exportApp(t)
			data := []byte("decoded file content")

			app.detailView.fileOps.exportDecoded("n", "out.txt", data)
			tt.end(t, app, p)

			if !bytes.Equal(data, make([]byte, len(data))) {
				t.Fatalf("%s: the decoded file must be wiped", tt.name)
			}
			app.Lock()
		})
	}
}

// writeFailingURIWriter is a fakeURIWriter whose writes fail.
type writeFailingURIWriter struct{ fakeURIWriter }

func (w *writeFailingURIWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestApp_LockDismissesOpenDialogs(t *testing.T) {
	a := test.NewTempApp(t)
	dir := t.TempDir()
	newVaultWith(t, dir)
	w := test.NewTempWindow(t, nil)
	app := newApp(a, w, settingsAt(a, dir), quietLog(), inlineRunner(), noTimers, testKDF)
	unlock(app)

	dialog.ShowInformation("open", "a dialog left open", w)
	app.Lock()

	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("locking must dismiss dialogs left open in the session")
	}
}
