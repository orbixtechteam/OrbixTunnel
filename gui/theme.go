package main

// forcedDark is a theme that is always dark regardless of the OS appearance,
// with a branded palette and a readable (not dimmed-to-invisible) disabled
// color so the log area stays legible.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

type forcedDark struct{}

var _ fyne.Theme = forcedDark{}

func (forcedDark) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	// Vercel-style monochrome: near-black canvas, off-white text/accents.
	switch n {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x0a, G: 0x0a, B: 0x0a, A: 0xff}
	case theme.ColorNameButton, theme.ColorNameInputBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 0x1a, G: 0x1a, B: 0x1a, A: 0xff}
	case theme.ColorNamePrimary, theme.ColorNameHyperlink, theme.ColorNameFocus:
		return color.NRGBA{R: 0xfa, G: 0xfa, B: 0xfa, A: 0xff}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xed, G: 0xed, B: 0xed, A: 0xff}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 0xa1, G: 0xa1, B: 0xa1, A: 0xff} // keep the log readable
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 0x70, G: 0x70, B: 0x70, A: 0xff}
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 0x26, G: 0x26, B: 0x26, A: 0xff}
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0x26, G: 0x26, B: 0x26, A: 0xff}
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (forcedDark) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (forcedDark) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (forcedDark) Size(n fyne.ThemeSizeName) float32       { return theme.DefaultTheme().Size(n) }
