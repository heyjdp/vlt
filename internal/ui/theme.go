package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// VltTheme is a custom theme that reduces all sizes by 2pt from the defaults.
type VltTheme struct{}

// Color delegates to the default theme.
func (t *VltTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
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

// Size returns the default size reduced by 2 for text sizes,
// and the default for non-text sizes.
func (t *VltTheme) Size(name fyne.ThemeSizeName) float32 {
	size := theme.DefaultTheme().Size(name)

	switch name {
	case theme.SizeNameText,
		theme.SizeNameHeadingText,
		theme.SizeNameSubHeadingText,
		theme.SizeNameCaptionText:
		return size - 2
	default:
		return size
	}
}
