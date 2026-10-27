package ui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

func TestVltTheme_ShrinksTextOnly(t *testing.T) {
	t.Parallel()
	th, def := &VltTheme{}, theme.DefaultTheme()

	if th.Size(theme.SizeNameText) != def.Size(theme.SizeNameText)-2 {
		t.Fatal("body text should be 2 points smaller than the default")
	}
	if th.Size(theme.SizeNamePadding) != def.Size(theme.SizeNamePadding) {
		t.Fatal("non-text sizes should be unchanged")
	}
	if th.Color(theme.ColorNameBackground, theme.VariantDark) != def.Color(theme.ColorNameBackground, theme.VariantDark) ||
		th.Font(fyne.TextStyle{}) != def.Font(fyne.TextStyle{}) ||
		th.Icon(theme.IconNameHome) != def.Icon(theme.IconNameHome) {
		t.Fatal("colours, fonts and icons should come from the default theme")
	}
}
