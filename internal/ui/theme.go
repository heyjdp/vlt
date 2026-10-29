package ui

import (
	_ "embed"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// appIconPNG is icon.png, a 256px copy of the repository's Icon.png.
//
//go:embed icon.png
var appIconPNG []byte

// AppIcon is vlt's icon (a safe door), used for the window, the header bar
// and the unlock and create screens.
var AppIcon = fyne.NewStaticResource("vlt.png", appIconPNG)

// colorNameSurface is the background of panels and cards, one step lighter
// (dark mode) or whiter (light mode) than the window. It is a built-in name
// so any theme, including fyne/test's, defines it.
const colorNameSurface = theme.ColorNameHeaderBackground

// palette holds one variant's colours.
type palette struct {
	background, surface, input, inputBorder, button, hover, pressed,
	separator, foreground, muted, placeholder, primary, onPrimary,
	disabledButton, selection, focus, scrollBar, overlay, shadow color.Color
}

func rgb(r, g, b uint8) color.NRGBA     { return color.NRGBA{R: r, G: g, B: b, A: 0xff} }
func rgba(r, g, b, a uint8) color.NRGBA { return color.NRGBA{R: r, G: g, B: b, A: a} }

// The accent is the teal of the app icon.
var (
	darkPalette = palette{
		background:     rgb(0x14, 0x17, 0x1c),
		surface:        rgb(0x1b, 0x1f, 0x26),
		input:          rgb(0x22, 0x27, 0x30),
		inputBorder:    rgb(0x2f, 0x35, 0x40),
		button:         rgb(0x27, 0x2c, 0x36),
		disabledButton: rgb(0x1f, 0x23, 0x2b),
		hover:          rgba(0xff, 0xff, 0xff, 0x0f),
		pressed:        rgba(0xff, 0xff, 0xff, 0x1f),
		separator:      rgb(0x29, 0x2e, 0x38),
		foreground:     rgb(0xe6, 0xe9, 0xee),
		muted:          rgb(0x87, 0x8f, 0x9c),
		placeholder:    rgb(0x6b, 0x73, 0x80),
		primary:        rgb(0x13, 0x7a, 0x73),
		onPrimary:      rgb(0xff, 0xff, 0xff),
		selection:      rgba(0x13, 0x7a, 0x73, 0x66),
		focus:          rgba(0x2d, 0xc4, 0xb8, 0x99),
		scrollBar:      rgba(0xff, 0xff, 0xff, 0x30),
		overlay:        rgb(0x1e, 0x22, 0x2a),
		shadow:         rgba(0x00, 0x00, 0x00, 0x66),
	}
	lightPalette = palette{
		background:     rgb(0xf3, 0xf5, 0xf7),
		surface:        rgb(0xff, 0xff, 0xff),
		input:          rgb(0xf7, 0xf8, 0xfa),
		inputBorder:    rgb(0xd9, 0xde, 0xe5),
		button:         rgb(0xe9, 0xec, 0xf0),
		disabledButton: rgb(0xf0, 0xf2, 0xf5),
		hover:          rgba(0x00, 0x00, 0x00, 0x0d),
		pressed:        rgba(0x00, 0x00, 0x00, 0x1a),
		separator:      rgb(0xe2, 0xe6, 0xeb),
		foreground:     rgb(0x1b, 0x20, 0x27),
		muted:          rgb(0x5f, 0x67, 0x73),
		placeholder:    rgb(0x80, 0x88, 0x95),
		primary:        rgb(0x0f, 0x76, 0x6e),
		onPrimary:      rgb(0xff, 0xff, 0xff),
		selection:      rgba(0x0f, 0x76, 0x6e, 0x2e),
		focus:          rgba(0x0f, 0x76, 0x6e, 0x99),
		scrollBar:      rgba(0x00, 0x00, 0x00, 0x30),
		overlay:        rgb(0xff, 0xff, 0xff),
		shadow:         rgba(0x00, 0x00, 0x00, 0x33),
	}
)

// VltTheme is vlt's theme: a teal accent on neutral greys, rounded inputs,
// and slightly smaller text than Fyne's defaults. Everything it does not set
// comes from the default theme.
type VltTheme struct{}

// Color returns vlt's palette colours, and the default theme's otherwise
// (error, warning and success keep Fyne's colours).
func (t *VltTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	p := &darkPalette
	if variant == theme.VariantLight {
		p = &lightPalette
	}
	switch name {
	case theme.ColorNameBackground:
		return p.background
	case colorNameSurface:
		return p.surface
	case theme.ColorNameInputBackground:
		return p.input
	case theme.ColorNameInputBorder:
		return p.inputBorder
	case theme.ColorNameButton:
		return p.button
	case theme.ColorNameDisabledButton:
		return p.disabledButton
	case theme.ColorNameHover:
		return p.hover
	case theme.ColorNamePressed:
		return p.pressed
	case theme.ColorNameSeparator:
		return p.separator
	case theme.ColorNameForeground:
		return p.foreground
	case theme.ColorNameDisabled:
		// Also the text of read-only entries (a saved secret's name and
		// group), so it stays readable.
		return p.muted
	case theme.ColorNamePlaceHolder:
		return p.placeholder
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return p.primary
	case theme.ColorNameForegroundOnPrimary:
		return p.onPrimary
	case theme.ColorNameSelection:
		return p.selection
	case theme.ColorNameFocus:
		return p.focus
	case theme.ColorNameScrollBar:
		return p.scrollBar
	case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground:
		return p.overlay
	case theme.ColorNameShadow:
		return p.shadow
	}
	return theme.DefaultTheme().Color(name, variant)
}

// Font delegates to the default theme.
func (t *VltTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

// Icon delegates to the default theme.
func (t *VltTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

// Size returns vlt's text sizes and corner radii, and the default theme's
// other sizes.
func (t *VltTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNameSubHeadingText:
		return 15
	case theme.SizeNameHeadingText:
		return 21
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 6
	case theme.SizeNameScrollBarRadius:
		return 4
	}
	return theme.DefaultTheme().Size(name)
}
