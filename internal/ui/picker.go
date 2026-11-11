package ui

import (
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
)

// picker shows the choosers for a file to open, a folder, or where to save
// a file: Fyne's dialogs in production (fynePicker), and in tests a fake
// the test answers. Each request is answered at most once, on the main
// goroutine, with the choice, with nil when the user cancels, or with an
// error; a request still open when the vault locks is never answered (the
// lock dismisses the dialog). operations wraps it (pickFile, pickFolder,
// pickSave), so no caller deals with those cases itself.
type picker interface {
	openFile(answer func(fyne.URIReadCloser, error))
	openFolder(answer func(fyne.ListableURI, error))
	// saveFile suggests name, and offers only files with its extension.
	saveFile(name string, answer func(fyne.URIWriteCloser, error))
}

// fynePicker is the production picker: Fyne's file dialogs over window.
type fynePicker struct{ window fyne.Window }

var _ picker = fynePicker{}

func (p fynePicker) openFile(answer func(fyne.URIReadCloser, error)) {
	dialog.NewFileOpen(answer, p.window).Show()
}

func (p fynePicker) openFolder(answer func(fyne.ListableURI, error)) {
	dialog.NewFolderOpen(answer, p.window).Show()
}

func (p fynePicker) saveFile(name string, answer func(fyne.URIWriteCloser, error)) {
	fd := dialog.NewFileSave(answer, p.window)
	fd.SetFileName(name)
	if ext := filepath.Ext(name); ext != "" {
		fd.SetFilter(storage.NewExtensionFileFilter([]string{ext}))
	}
	fd.Show()
}
