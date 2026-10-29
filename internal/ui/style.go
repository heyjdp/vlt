package ui

import (
	"image/color"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/heyjdp/vlt/internal/store"
)

// Icons that Fyne's theme lacks (Material Design paths, as Fyne's own).
var (
	lockIcon = theme.NewThemedResource(fyne.NewStaticResource("lock.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M18 8h-1V6c0-2.76-2.24-5-5-5S7 3.24 7 6v2H6c-1.1 0-2 .9-2 2v10c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V10c0-1.1-.9-2-2-2zm-6 9c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2zm3.1-9H8.9V6c0-1.71 1.39-3.1 3.1-3.1 1.71 0 3.1 1.39 3.1 3.1v2z"/></svg>`)))
	keyIcon = theme.NewThemedResource(fyne.NewStaticResource("key.svg", []byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M12.65 10C11.83 7.67 9.61 6 7 6c-3.31 0-6 2.69-6 6s2.69 6 6 6c2.61 0 4.83-1.67 5.65-4H17v4h4v-4h2v-4H12.65zM7 14c-1.1 0-2-.9-2-2s.9-2 2-2 2 .9 2 2-.9 2-2 2z"/></svg>`)))
)

// displayLocation shortens a vault location for display: a local path in
// the home folder is shown as ~/…; anything else is shown as given.
func displayLocation(location string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return location
	}
	if rel, err := filepath.Rel(home, location); err == nil && rel != "." && filepath.IsLocal(rel) {
		return "~" + string(filepath.Separator) + rel
	}
	return location
}

// typeIcon returns the icon shown for a secret type.
func typeIcon(t store.SecretType) fyne.Resource {
	switch t {
	case store.TypePassword:
		return keyIcon
	case store.TypeRecord:
		return theme.AccountIcon()
	case store.TypeFile:
		return theme.FileIcon()
	}
	return theme.QuestionIcon()
}

// typeName returns a secret type's display name.
func typeName(t store.SecretType) string {
	switch t {
	case store.TypePassword:
		return "Password"
	case store.TypeRecord:
		return "Record"
	case store.TypeFile:
		return "File"
	}
	return string(t)
}

// surface is a rounded rectangle filled with a theme colour. It is a widget
// so its colour follows a change between light and dark mode.
type surface struct {
	widget.BaseWidget
	color  fyne.ThemeColorName
	radius float32
}

func newSurface(c fyne.ThemeColorName, radius float32) *surface {
	s := &surface{color: c, radius: radius}
	s.ExtendBaseWidget(s)
	return s
}

// CreateRenderer implements fyne.Widget.
func (s *surface) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.Transparent)
	r := &surfaceRenderer{s: s, rect: rect}
	r.Refresh()
	return r
}

type surfaceRenderer struct {
	s    *surface
	rect *canvas.Rectangle
}

func (r *surfaceRenderer) Layout(size fyne.Size)        { r.rect.Resize(size) }
func (r *surfaceRenderer) MinSize() fyne.Size           { return fyne.Size{} }
func (r *surfaceRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.rect} }
func (r *surfaceRenderer) Destroy()                     {}

func (r *surfaceRenderer) Refresh() {
	r.rect.FillColor = theme.ColorForWidget(r.s.color, r.s)
	r.rect.StrokeColor = theme.ColorForWidget(theme.ColorNameSeparator, r.s)
	if r.s.radius > 0 {
		r.rect.StrokeWidth = 1
	} else {
		r.rect.StrokeWidth = 0
	}
	r.rect.CornerRadius = r.s.radius
	r.rect.Refresh()
}

// card places content on a rounded surface with generous padding. It is a
// plain container, so the activity tracker and blankEntries reach the
// widgets inside it.
func card(content ...fyne.CanvasObject) *fyne.Container {
	inner := container.NewVBox(content...)
	return container.NewStack(newSurface(colorNameSurface, 10), padded(inner, cardPadding))
}

// panelBackground fills a whole panel (sidebar, header) with the surface
// colour, without corners or a border.
func panelBackground(content fyne.CanvasObject) *fyne.Container {
	return container.NewStack(newSurface(colorNameSurface, 0), content)
}

// padded surrounds obj with p points of space on every side.
func padded(obj fyne.CanvasObject, p float32) *fyne.Container {
	return container.New(layout.NewCustomPaddedLayout(p, p, p, p), obj)
}

// heading returns a large bold label.
func heading(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.SizeName = theme.SizeNameHeadingText
	l.TextStyle = fyne.TextStyle{Bold: true}
	l.Truncation = fyne.TextTruncateEllipsis
	return l
}

// subheading returns a medium bold label (a section title).
func subheading(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.SizeName = theme.SizeNameSubHeadingText
	l.TextStyle = fyne.TextStyle{Bold: true}
	return l
}

// muted returns a wrapping label in the theme's secondary text colour.
func muted(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	l.Wrapping = fyne.TextWrapWord
	return l
}

// caption returns a small label in the secondary text colour.
func caption(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	l.SizeName = theme.SizeNameCaptionText
	return l
}

// errorText returns a hidden, wrapping label in the theme's error colour.
func errorText() *widget.Label {
	l := widget.NewLabel("")
	l.Importance = widget.DangerImportance
	l.Wrapping = fyne.TextWrapWord
	l.Hide()
	return l
}

// fieldLabel returns a form row's label.
func fieldLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Importance = widget.LowImportance
	return l
}

// brandMark returns the app icon at size points square.
func brandMark(size float32) *canvas.Image {
	img := canvas.NewImageFromResource(AppIcon)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(size, size))
	return img
}

// fixedWidth makes obj at least width points wide.
func fixedWidth(obj fyne.CanvasObject, width float32) *fyne.Container {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 0))
	return container.NewStack(spacer, obj)
}

// cardPadding is the space between a card's edge and its content.
const cardPadding = 14

// centeredCard is the layout of the unlock, create and error screens: a
// card of the given width in the middle of the window.
func centeredCard(width float32, content ...fyne.CanvasObject) *fyne.Container {
	column := container.New(&columnLayout{width: width - 2*cardPadding}, content...)
	return container.NewCenter(container.NewStack(newSurface(colorNameSurface, 10), padded(column, cardPadding)))
}

// columnLayout stacks objects vertically in a column of fixed width. Its
// minimum height is measured with every object at that width, so wrapped
// labels report their real height and never clip (a VBox in a Center
// measures them before they are given a width).
type columnLayout struct{ width float32 }

// MinSize implements fyne.Layout.
func (l *columnLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	pad := theme.Padding()
	height, shown := float32(0), 0
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		o.Resize(fyne.NewSize(l.width, o.Size().Height))
		height += o.MinSize().Height
		shown++
	}
	if shown > 1 {
		height += pad * float32(shown-1)
	}
	return fyne.NewSize(l.width, height)
}

// Layout implements fyne.Layout.
func (l *columnLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	pad := theme.Padding()
	y := float32(0)
	for _, o := range objects {
		if !o.Visible() {
			continue
		}
		h := o.MinSize().Height
		o.Move(fyne.NewPos(0, y))
		o.Resize(fyne.NewSize(size.Width, h))
		y += h + pad
	}
}

// emptyState is a centred icon, title and hint shown when a panel has
// nothing to display.
func emptyState(icon fyne.Resource, title, hint string) *fyne.Container {
	img := widget.NewIcon(icon)
	iconBox := container.NewCenter(fixedHeight(fixedWidth(img, 48), 48))
	t := subheading(title)
	t.Alignment = fyne.TextAlignCenter
	h := muted(hint)
	h.Alignment = fyne.TextAlignCenter
	return container.NewCenter(fixedWidth(container.NewVBox(iconBox, t, h), 320))
}

// fixedHeight makes obj at least height points tall.
func fixedHeight(obj fyne.CanvasObject, height float32) *fyne.Container {
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(0, height))
	return container.NewStack(spacer, obj)
}
