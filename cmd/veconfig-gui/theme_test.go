package main

import (
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2/theme"
)

// luminance is the WCAG 2 relative luminance of an opaque colour.
func luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrast is the WCAG 2 contrast ratio of two opaque colours.
func contrast(a, b color.NRGBA) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// over blends c over the opaque background bg.
func over(c, bg color.NRGBA) color.NRGBA {
	a := float64(c.A) / 255
	mix := func(x, y uint8) uint8 { return uint8(math.Round(float64(x)*a + float64(y)*(1-a))) }
	return color.NRGBA{R: mix(c.R, bg.R), G: mix(c.G, bg.G), B: mix(c.B, bg.B), A: 0xff}
}

// TestThemeContrast requires WCAG AA contrast (4.5:1) for text in both
// variants, and reports the contrast of borders and disabled text, which
// WCAG does not require.
func TestThemeContrast(t *testing.T) {
	for _, v := range []struct {
		name string
		k    tokens
	}{{"light", lightTokens}, {"dark", darkTokens}} {
		k := v.k
		text := []struct {
			what   string
			fg, bg color.NRGBA
		}{
			{"foreground on background", k.foreground, k.background},
			{"foreground on surface", k.foreground, k.surface},
			{"foreground on input", k.foreground, k.input},
			{"muted on surface", k.muted, k.surface},
			{"muted on background", k.muted, k.background},
			{"placeholder (muted) on input", k.muted, k.input},
			{"on primary on primary", k.onPrimary, k.primary},
			{"primary on surface", k.primary, k.surface},
			// Error and success also colour text; warning (and "changed") is
			// only a fill, with text in its own colour on it.
			{"error on surface", k.errorColor, k.surface},
			{"success on surface", k.success, k.surface},
			{"on warning on warning", k.onWarning, k.warning},
			{"on error on error", k.onSemantic, k.errorColor},
			{"on success on success", k.onSemantic, k.success},
			// Pills that are not solid have foreground text on a tint.
			{"warning pill", k.foreground, over(withAlpha(k.warning, pillTint), k.surface)},
			{"error pill", k.foreground, over(withAlpha(k.errorColor, pillTint), k.surface)},
			{"success pill", k.foreground, over(withAlpha(k.success, pillTint), k.surface)},
			{"primary pill", k.foreground, over(withAlpha(k.primary, pillTint), k.surface)},
			{"foreground on selection", k.foreground, over(withAlpha(k.primary, 0x33), k.surface)},
		}
		for _, p := range text {
			c := contrast(p.fg, p.bg)
			t.Logf("%s: %s %.2f:1", v.name, p.what, c)
			if c < 4.5 {
				t.Errorf("%s: %s has contrast %.2f:1, want at least 4.5:1", v.name, p.what, c)
			}
		}
		// WCAG 1.4.11: the outline of a control needs 3:1.
		for _, p := range []struct {
			what   string
			fg, bg color.NRGBA
		}{
			{"input border on input", k.inputBorder, k.input},
			{"input border on surface", k.inputBorder, k.surface},
		} {
			c := contrast(p.fg, p.bg)
			t.Logf("%s: %s %.2f:1", v.name, p.what, c)
			if c < 3 {
				t.Errorf("%s: %s has contrast %.2f:1, want at least 3:1", v.name, p.what, c)
			}
		}
		for _, p := range []struct {
			what   string
			fg, bg color.NRGBA
		}{
			{"separator on surface", k.border, k.surface},
			{"separator on background", k.border, k.background},
			{"disabled on surface", k.disabled, k.surface},
			{"disabled on disabled button", k.disabled, k.disabledButton},
			{"button on background", k.button, k.background},
		} {
			t.Logf("%s: %s %.2f:1 (not required)", v.name, p.what, contrast(p.fg, p.bg))
		}
	}
}

// TestThemeVariants checks that a chosen variant overrides the OS's and
// that the theme has the colour for changes.
func TestThemeVariants(t *testing.T) {
	light := newAppTheme("light")
	if got := light.Color(colorNamePage, theme.VariantDark); got != lightTokens.background {
		t.Errorf("light theme page background in the dark OS variant = %v, want %v", got, lightTokens.background)
	}
	sys := newAppTheme("")
	if got := sys.Color(colorNamePage, theme.VariantDark); got != darkTokens.background {
		t.Errorf("system theme page background in the dark OS variant = %v, want %v", got, darkTokens.background)
	}
	if got := sys.Color(theme.ColorNameBackground, theme.VariantLight); got != lightTokens.surface {
		t.Errorf("Fyne background (cards, window) = %v, want the surface colour %v", got, lightTokens.surface)
	}
	if got := sys.Color(colorNameChanged, theme.VariantLight); got != lightTokens.warning {
		t.Errorf("changed colour = %v, want the warning colour %v", got, lightTokens.warning)
	}
}

// TestThemeMenu checks that Options > Theme saves the choice and applies
// it at once.
func TestThemeMenu(t *testing.T) {
	u := newTestUI(t)
	m := themeMenu(u)
	if len(m.Items) != 3 || !m.Items[0].Checked {
		t.Fatalf("theme menu %d items, first checked %v; want System, Light, Dark with System checked", len(m.Items), m.Items[0].Checked)
	}
	m.Items[2].Action()
	if got := u.app.Preferences().String(prefTheme); got != "dark" || !m.Items[2].Checked || m.Items[0].Checked {
		t.Errorf("after choosing Dark: preference %q, checked %v/%v", got, m.Items[0].Checked, m.Items[2].Checked)
	}
	if th, ok := u.app.Settings().Theme().(*appTheme); !ok || !th.forced || th.variant != theme.VariantDark {
		t.Errorf("applied theme = %#v, want the forced dark appTheme", u.app.Settings().Theme())
	}
}
