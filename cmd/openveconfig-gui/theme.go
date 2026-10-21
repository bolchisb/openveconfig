package main

// The app's own theme: cool slate surfaces, a blue accent and amber for
// unsent changes, in a light and a dark variant. The text font is Inter
// (fonts/, SIL Open Font License); icons are Fyne's.

import (
	_ "embed"
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
	// changes bar, cards, menus and dialogs.
	colorNameSurface fyne.ThemeColorName = "surface"
	// colorNameSidebar is the background of the sidebar.
	colorNameSidebar fyne.ThemeColorName = "sidebar"
	// colorNameCardShadow is the soft shadow under cards.
	colorNameCardShadow fyne.ThemeColorName = "cardShadow"
	// colorNameTint is the accent's tint behind icons on cards.
	colorNameTint fyne.ThemeColorName = "tint"
	// colorNameHeroStart and colorNameHeroEnd are the ends of the gradient
	// of the start screen's banner, with white text on it.
	colorNameHeroStart fyne.ThemeColorName = "heroStart"
	colorNameHeroEnd   fyne.ThemeColorName = "heroEnd"
	// colorNameOnHero is the text on the banner: white in both variants.
	colorNameOnHero fyne.ThemeColorName = "onHero"
	// colorNameChanged marks a value or a page with unsent changes.
	colorNameChanged fyne.ThemeColorName = "changed"
	// colorNameZoneOK, colorNameZoneWarn and colorNameZoneOff are the zones
	// of the charts' voltage scales: normal, a warning, and off. Blue,
	// yellow and red (after the Okabe-Ito palette) stay apart for readers
	// with red-green colour blindness, unlike green and red.
	colorNameZoneOK   fyne.ThemeColorName = "zoneOK"
	colorNameZoneWarn fyne.ThemeColorName = "zoneWarn"
	colorNameZoneOff  fyne.ThemeColorName = "zoneOff"
	// colorNameZoneFloat and colorNameZoneAbsorb split the normal zone of the
	// battery scale into the charge-target steps: the float band and the
	// absorption band. Bluish green and reddish purple (also Okabe-Ito) stay
	// apart from the blue normal zone and from each other for colour-blind
	// readers.
	colorNameZoneFloat  fyne.ThemeColorName = "zoneFloat"
	colorNameZoneAbsorb fyne.ThemeColorName = "zoneAbsorb"
	// colorNameSeriesVoltage and colorNameSeriesCurrent are the two lines of
	// the charge profile; vermilion and blue stay apart for colour-blind
	// readers.
	colorNameSeriesVoltage fyne.ThemeColorName = "seriesVoltage"
	colorNameSeriesCurrent fyne.ThemeColorName = "seriesCurrent"
	// colorNameLEDGreen, colorNameLEDAmber and colorNameLEDRed are the LEDs
	// of the remote panel, in the colours of a physical panel.
	colorNameLEDGreen fyne.ThemeColorName = "ledGreen"
	colorNameLEDAmber fyne.ThemeColorName = "ledAmber"
	colorNameLEDRed   fyne.ThemeColorName = "ledRed"
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
	sidebar, heroStart, heroEnd        color.NRGBA
	inputBorder                        color.NRGBA // outline of entries, check boxes and radio buttons
	foreground, muted, disabled        color.NRGBA
	button, disabledButton             color.NRGBA
	primary, onPrimary                 color.NRGBA
	warning, errorColor, success       color.NRGBA
	onSemantic                         color.NRGBA // text on error and success
	onWarning                          color.NRGBA // text on warning
	ledGreen, ledAmber, ledRed         color.NRGBA // remote panel LEDs
	zoneOK, zoneWarn, zoneOff          color.NRGBA // chart zones (colorNameZoneOK …)
	zoneFloat, zoneAbsorb              color.NRGBA // charge-target bands of the battery scale
	seriesVoltage, seriesCurrent       color.NRGBA // charge profile lines
	scrollBarBackground                color.NRGBA
	hover, pressed, shadow             uint8 // opacity of the foreground, the foreground and black
	cardShadow, tint                   uint8 // opacity of black under cards, and of the accent behind icons
}

var (
	lightTokens = tokens{
		background: rgb(0xF3F6FB), surface: rgb(0xFFFFFF), input: rgb(0xFFFFFF),
		border: rgb(0xE2E8F0), inputBorder: rgb(0x8291A7),
		sidebar: rgb(0xFAFBFD), heroStart: rgb(0x1D4ED8), heroEnd: rgb(0x4F46E5),
		foreground: rgb(0x0F172A), muted: rgb(0x526077), disabled: rgb(0x94A3B8),
		button: rgb(0xE8EFFD), disabledButton: rgb(0xF1F4F9),
		primary: rgb(0x2563EB), onPrimary: rgb(0xFFFFFF),
		warning: rgb(0xF59E0B), errorColor: rgb(0xDC2626), success: rgb(0x0F766E), onSemantic: rgb(0xFFFFFF),
		onWarning: rgb(0x0F172A), ledGreen: rgb(0x047857), ledAmber: rgb(0xD97706), ledRed: rgb(0xDC2626),
		zoneOK: rgb(0x0072B2), zoneWarn: rgb(0xE6B400), zoneOff: rgb(0xD62828),
		zoneFloat: rgb(0x009E73), zoneAbsorb: rgb(0xCC79A7),
		seriesVoltage: rgb(0xD55E00), seriesCurrent: rgb(0x0072B2),
		scrollBarBackground: rgb(0xEEF2F7), hover: 0x0D, pressed: 0x1A, shadow: 0x33,
		cardShadow: 0x12, tint: 0x1F,
	}
	darkTokens = tokens{
		background: rgb(0x0B1120), surface: rgb(0x131B2C), input: rgb(0x0E1626),
		border: rgb(0x23304A), inputBorder: rgb(0x60718D),
		sidebar: rgb(0x0F1729), heroStart: rgb(0x1E40AF), heroEnd: rgb(0x4338CA),
		foreground: rgb(0xE6ECF5), muted: rgb(0x94A3B8), disabled: rgb(0x5F6F88),
		button: rgb(0x1C2A47), disabledButton: rgb(0x161F33),
		primary: rgb(0x60A5FA), onPrimary: rgb(0x0B1120),
		warning: rgb(0xFBBF24), errorColor: rgb(0xF87171), success: rgb(0x2DD4BF), onSemantic: rgb(0x0B1120),
		onWarning: rgb(0x0B1120), ledGreen: rgb(0x34D399), ledAmber: rgb(0xF59E0B), ledRed: rgb(0xF87171),
		zoneOK: rgb(0x56B4E9), zoneWarn: rgb(0xF0E442), zoneOff: rgb(0xFF5C5C),
		zoneFloat: rgb(0x4FD6A8), zoneAbsorb: rgb(0xE59BCB),
		seriesVoltage: rgb(0xE8632A), seriesCurrent: rgb(0x3A94CF),
		scrollBarBackground: rgb(0x162036), hover: 0x14, pressed: 0x29, shadow: 0x80,
		cardShadow: 0x59, tint: 0x29,
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
		colorNameSidebar:                         k.sidebar,
		colorNameCardShadow:                      withAlpha(rgb(0), k.cardShadow),
		colorNameTint:                            withAlpha(k.primary, k.tint),
		colorNameHeroStart:                       k.heroStart,
		colorNameHeroEnd:                         k.heroEnd,
		colorNameOnHero:                          rgb(0xFFFFFF),
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
		colorNameZoneFloat:                       k.zoneFloat,
		colorNameZoneAbsorb:                      k.zoneAbsorb,
		colorNameSeriesVoltage:                   k.seriesVoltage,
		colorNameSeriesCurrent:                   k.seriesCurrent,
		colorNameLEDGreen:                        k.ledGreen,
		colorNameLEDAmber:                        k.ledAmber,
		colorNameLEDRed:                          k.ledRed,
		theme.ColorNameForegroundOnWarning:       k.onWarning,
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

// The Inter fonts: Regular for text, SemiBold for bold. Italic and
// monospace text keep Fyne's fonts; the app shows no italic text.
const fontRegularFile, fontSemiBoldFile = "Inter-Regular.ttf", "Inter-SemiBold.ttf"

var (
	//go:embed fonts/Inter-Regular.ttf
	interRegular []byte
	//go:embed fonts/Inter-SemiBold.ttf
	interSemiBold []byte

	fontRegular  = fyne.NewStaticResource(fontRegularFile, interRegular)
	fontSemiBold = fyne.NewStaticResource(fontSemiBoldFile, interSemiBold)
)

func (t *appTheme) Font(s fyne.TextStyle) fyne.Resource {
	switch {
	case s.Monospace, s.Symbol, s.Italic:
		return theme.DefaultTheme().Font(s)
	case s.Bold:
		return fontSemiBold
	}
	return fontRegular
}

func (t *appTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

func (t *appTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 13
	case theme.SizeNameHeadingText:
		return 22
	case theme.SizeNameSubHeadingText:
		return 15
	case theme.SizeNameCaptionText:
		return 11.5
	case theme.SizeNamePadding:
		return 4
	case theme.SizeNameInnerPadding:
		return 9
	case theme.SizeNameInlineIcon:
		return 18
	case theme.SizeNameInputRadius, theme.SizeNameButtonRadius, theme.SizeNameSelectionRadius:
		return 8
	case theme.SizeNameCardRadius:
		return 12
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
