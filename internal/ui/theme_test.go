package ui

import (
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// luminance is a colour's WCAG relative luminance.
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	channel := func(v uint32) float64 {
		s := float64(v) / 0xffff
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

// contrast is the WCAG contrast ratio of two opaque colours.
func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestVltTheme_TextIsReadableInBothVariants(t *testing.T) {
	t.Parallel()
	th := &VltTheme{}
	for _, v := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
		c := func(n fyne.ThemeColorName) color.Color { return th.Color(n, v) }
		checks := []struct {
			what   string
			fg, bg fyne.ThemeColorName
			min    float64
		}{
			{"text on the window", theme.ColorNameForeground, theme.ColorNameBackground, 7},
			{"text on a card", theme.ColorNameForeground, colorNameSurface, 7},
			{"text in an entry", theme.ColorNameForeground, theme.ColorNameInputBackground, 7},
			{"secondary text on a card", theme.ColorNameDisabled, colorNameSurface, 4.5},
			{"secondary text on the window", theme.ColorNameDisabled, theme.ColorNameBackground, 4.5},
			{"read-only entry text", theme.ColorNameDisabled, theme.ColorNameInputBackground, 4.5},
			{"disabled button text", theme.ColorNameDisabled, theme.ColorNameDisabledButton, 4.5},
			{"placeholder text", theme.ColorNamePlaceHolder, theme.ColorNameInputBackground, 3},
			{"button text on the accent", theme.ColorNameForegroundOnPrimary, theme.ColorNamePrimary, 4.5},
		}
		for _, ch := range checks {
			if got := contrast(c(ch.fg), c(ch.bg)); got < ch.min {
				t.Errorf("variant %d: %s has contrast %.2f, want at least %.1f", v, ch.what, got, ch.min)
			}
		}
	}
}

func TestVltTheme_SizesAndFallbacks(t *testing.T) {
	t.Parallel()
	th, def := &VltTheme{}, theme.DefaultTheme()

	if th.Size(theme.SizeNameText) >= def.Size(theme.SizeNameText) {
		t.Fatal("body text should be smaller than Fyne's default")
	}
	if th.Size(theme.SizeNameHeadingText) <= th.Size(theme.SizeNameSubHeadingText) ||
		th.Size(theme.SizeNameSubHeadingText) <= th.Size(theme.SizeNameText) ||
		th.Size(theme.SizeNameText) <= th.Size(theme.SizeNameCaptionText) {
		t.Fatal("text sizes must run heading > subheading > body > caption")
	}
	if th.Size(theme.SizeNamePadding) != def.Size(theme.SizeNamePadding) {
		t.Fatal("spacing should come from the default theme")
	}
	// Error, warning and success keep Fyne's colours, so dialogs and the
	// Delete button look as users expect.
	for _, n := range []fyne.ThemeColorName{theme.ColorNameError, theme.ColorNameWarning, theme.ColorNameSuccess} {
		if th.Color(n, theme.VariantDark) != def.Color(n, theme.VariantDark) {
			t.Fatalf("%s should come from the default theme", n)
		}
	}
	if th.Font(fyne.TextStyle{}) != def.Font(fyne.TextStyle{}) || th.Icon(theme.IconNameHome) != def.Icon(theme.IconNameHome) {
		t.Fatal("fonts and icons should come from the default theme")
	}
	if th.Color(theme.ColorNamePrimary, theme.VariantDark) == def.Color(theme.ColorNamePrimary, theme.VariantDark) {
		t.Fatal("the accent should be vlt's teal, not Fyne's blue")
	}
}
