package main

// The app's own theme: neutral surfaces, a teal accent and amber for unsent
// changes, in a light and a dark variant. Fonts and icons are Fyne's.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Theme colour names of our own, next to Fyne's theme.ColorName*.
const (
	// colorNamePage is the backdrop of the page area and the start screen,
	// on which cards stand out.
	colorNamePage fyne.ThemeColorName = "page"
	// colorNameSurface is the background of raised areas: the header, the
	// sidebar, the changes bar, cards, menus and dialogs.
	colorNameSurface fyne.ThemeColorName = "surface"
	// colorNameChanged marks a value or a page with unsent changes.
	colorNameChanged fyne.ThemeColorName = "changed"
	// colorNameZoneOK, colorNameZoneWarn and colorNameZoneOff are the zones
	// of the charts' voltage scales: normal, a warning, and off. Blue,
	// yellow and red (after the Okabe-Ito palette) stay apart for readers
	// with red-green colour blindness, unlike green and red.
	colorNameZoneOK   fyne.ThemeColorName = "zoneOK"
	colorNameZoneWarn fyne.ThemeColorName = "zoneWarn"
	colorNameZoneOff  fyne.ThemeColorName = "zoneOff"
)

// prefTheme is the preference that holds the chosen theme variant: "light",
// "dark", or "" to follow the OS.
const prefTheme = "theme"

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// withAlpha returns c at opacity a.
func withAlpha(c color.NRGBA, a uint8) color.NRGBA {
	c.A = a
	return c
}

// tokens are the colours of one variant of the theme. Fyne fills cards,
// the window and the menu bar with theme.ColorNameBackground, so that is
// the surface colour; background is the backdrop of the pages
// (colorNamePage).
type tokens struct {
	background, surface, input, border color.NRGBA
	inputBorder                        color.NRGBA // outline of entries, check boxes and radio buttons
	foreground, muted, disabled        color.NRGBA
	button, disabledButton             color.NRGBA
	primary, onPrimary                 color.NRGBA
	warning, errorColor, success       color.NRGBA
	onSemantic                         color.NRGBA // text on warning, error and success
	zoneOK, zoneWarn, zoneOff          color.NRGBA // chart zones (colorNameZoneOK …)
	scrollBarBackground                color.NRGBA
	hover, pressed, shadow             uint8 // opacity of the foreground, the foreground and black
}

var (
	lightTokens = tokens{
		background: rgb(0xF4F6F7), surface: rgb(0xFFFFFF), input: rgb(0xF0F3F4),
		border: rgb(0xD5DCDF), inputBorder: rgb(0x7F8C93),
		foreground: rgb(0x11181C), muted: rgb(0x5B6970), disabled: rgb(0x87949A),
		button: rgb(0xE6EBED), disabledButton: rgb(0xEEF1F2),
		primary: rgb(0x0F766E), onPrimary: rgb(0xFFFFFF),
		warning: rgb(0xB45309), errorColor: rgb(0xDC2626), success: rgb(0x047857), onSemantic: rgb(0xFFFFFF),
		zoneOK: rgb(0x0072B2), zoneWarn: rgb(0xE6B400), zoneOff: rgb(0xD62828),
		scrollBarBackground: rgb(0xE6EBED), hover: 0x0F, pressed: 0x1F, shadow: 0x33,
	}
	darkTokens = tokens{
		background: rgb(0x0F1417), surface: rgb(0x171D21), input: rgb(0x1E262B),
		border: rgb(0x2A343A), inputBorder: rgb(0x66747B),
		foreground: rgb(0xE6EDF0), muted: rgb(0x93A1A8), disabled: rgb(0x66747B),
		button: rgb(0x263036), disabledButton: rgb(0x1B2226),
		primary: rgb(0x14B8A6), onPrimary: rgb(0x0F1417),
		warning: rgb(0xF59E0B), errorColor: rgb(0xF87171), success: rgb(0x34D399), onSemantic: rgb(0x0F1417),
		zoneOK: rgb(0x56B4E9), zoneWarn: rgb(0xF0E442), zoneOff: rgb(0xFF5C5C),
		scrollBarBackground: rgb(0x1E262B), hover: 0x14, pressed: 0x29, shadow: 0x80,
	}
	lightPalette = lightTokens.palette()
	darkPalette  = darkTokens.palette()
)

// palette maps theme colour names to the colours of one variant.
func (k tokens) palette() map[fyne.ThemeColorName]color.Color {
	return map[fyne.ThemeColorName]color.Color{
		colorNamePage:                            k.background,
		theme.ColorNameBackground:                k.surface,
		colorNameSurface:                         k.surface,
		theme.ColorNameOverlayBackground:         k.surface,
		theme.ColorNameMenuBackground:            k.surface,
		theme.ColorNameHeaderBackground:          k.surface,
		theme.ColorNameInputBackground:           k.input,
		theme.ColorNameInputBorder:               k.inputBorder,
		theme.ColorNameSeparator:                 k.border,
		theme.ColorNameForeground:                k.foreground,
		theme.ColorNamePlaceHolder:               k.muted,
		theme.ColorNameDisabled:                  k.disabled,
		theme.ColorNameButton:                    k.button,
		theme.ColorNameDisabledButton:            k.disabledButton,
		theme.ColorNamePrimary:                   k.primary,
		theme.ColorNameHyperlink:                 k.primary,
		theme.ColorNameForegroundOnPrimary:       k.onPrimary,
		theme.ColorNameFocus:                     withAlpha(k.primary, 0x66),
		theme.ColorNameSelection:                 withAlpha(k.primary, 0x33),
		theme.ColorNameHover:                     withAlpha(k.foreground, k.hover),
		theme.ColorNamePressed:                   withAlpha(k.foreground, k.pressed),
		theme.ColorNameWarning:                   k.warning,
		colorNameChanged:                         k.warning,
		theme.ColorNameError:                     k.errorColor,
		theme.ColorNameSuccess:                   k.success,
		colorNameZoneOK:                          k.zoneOK,
		colorNameZoneWarn:                        k.zoneWarn,
		colorNameZoneOff:                         k.zoneOff,
		theme.ColorNameForegroundOnWarning:       k.onSemantic,
		theme.ColorNameForegroundOnError:         k.onSemantic,
		theme.ColorNameForegroundOnSuccess:       k.onSemantic,
		theme.ColorNameShadow:                    withAlpha(rgb(0), k.shadow),
		theme.ColorNameScrollBar:                 withAlpha(k.foreground, 0x66),
		theme.ColorNameScrollBarBackground:       k.scrollBarBackground,
		theme.ColorNameInnerWindowBorder:         k.surface,
		theme.ColorNameInnerWindowBorderInactive: k.background,
	}
}

// appTheme is the theme of the app. A zero variant follows the OS.
type appTheme struct {
	// forced is set when the user chose a variant instead of the OS's.
	forced  bool
	variant fyne.ThemeVariant
}

// newAppTheme returns the theme for a prefTheme value.
func newAppTheme(pref string) *appTheme {
	switch pref {
	case "light":
		return &appTheme{forced: true, variant: theme.VariantLight}
	case "dark":
		return &appTheme{forced: true, variant: theme.VariantDark}
	}
	return &appTheme{}
}

func (t *appTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if t.forced {
		v = t.variant
	}
	p := darkPalette
	if v == theme.VariantLight {
		p = lightPalette
	}
	if c, ok := p[n]; ok {
		return c
	}
	return theme.DefaultTheme().Color(n, v)
}

func (t *appTheme) Font(s fyne.TextStyle) fyne.Resource { return theme.DefaultTheme().Font(s) }

func (t *appTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

func (t *appTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameSubHeadingText:
		return 16
	case theme.SizeNameCaptionText:
		return 11
	case theme.SizeNamePadding:
		return 4
	case theme.SizeNameInnerPadding:
		return 8
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameInputRadius, theme.SizeNameButtonRadius, theme.SizeNameSelectionRadius:
		return 6
	case theme.SizeNameCardRadius:
		return 8
	}
	return theme.DefaultTheme().Size(n)
}

// applyTheme sets the theme saved in the preferences of a.
func applyTheme(a fyne.App) {
	a.Settings().SetTheme(newAppTheme(a.Preferences().String(prefTheme)))
}

// themeMenu offers the OS's variant, light and dark. A choice is saved and
// applied at once.
func themeMenu(u *ui) *fyne.Menu {
	m := fyne.NewMenu("")
	add := func(label, pref string) {
		item := fyne.NewMenuItem(label, nil)
		item.Checked = u.app.Preferences().String(prefTheme) == pref
		item.Action = func() {
			u.app.Preferences().SetString(prefTheme, pref)
			applyTheme(u.app)
			for _, it := range m.Items {
				it.Checked = it == item
			}
			m.Refresh()
		}
		m.Items = append(m.Items, item)
	}
	add(tr("System"), "")
	add(tr("Light"), "light")
	add(tr("Dark"), "dark")
	return m
}
