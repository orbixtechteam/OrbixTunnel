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
	switch n {
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x0e, G: 0x1b, B: 0x2e, A: 0xff}
	case theme.ColorNameButton, theme.ColorNameInputBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return color.NRGBA{R: 0x17, G: 0x2a, B: 0x40, A: 0xff}
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		return color.NRGBA{R: 0x3d, G: 0xd6, B: 0xb0, A: 0xff}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xe6, G: 0xed, B: 0xf3, A: 0xff}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 0x9a, G: 0xb0, B: 0xc4, A: 0xff} // keep the log readable
	case theme.ColorNamePlaceHolder:
		return color.NRGBA{R: 0x7a, G: 0x8a, B: 0x9c, A: 0xff}
	case theme.ColorNameSeparator:
		return color.NRGBA{R: 0x24, G: 0x38, B: 0x50, A: 0xff}
	}
	return theme.DefaultTheme().Color(n, theme.VariantDark)
}

func (forcedDark) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (forcedDark) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }
func (forcedDark) Size(n fyne.ThemeSizeName) float32       { return theme.DefaultTheme().Size(n) }
