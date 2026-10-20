package main

// Localization. See docs/adr-0001-localization.md for the decision and the
// rules every UI string must follow.

import (
	"embed"
	"fmt"
	"maps"

	"fyne.io/fyne/v2/lang"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// translationFiles are the catalogs, one per language, named <tag>.json.
//
//go:embed translations/*.json
var translationFiles embed.FS

// prefLanguage is the preference that holds the chosen UI language as a
// BCP 47 tag; empty means the OS locale.
const prefLanguage = "language"

var (
	bundle    = loadTranslations()
	localizer *i18n.Localizer
	printer   *message.Printer
)

func init() { setLanguage("en") }

// loadTranslations parses the embedded catalogs. A broken catalog is a
// build mistake, caught by the tests, so it panics.
func loadTranslations() *i18n.Bundle {
	b := i18n.NewBundle(language.English)
	files, err := translationFiles.ReadDir("translations")
	if err != nil {
		panic(err)
	}
	for _, f := range files {
		data, err := translationFiles.ReadFile("translations/" + f.Name())
		if err != nil {
			panic(err)
		}
		if _, err := b.ParseMessageFileBytes(data, f.Name()); err != nil {
			panic(fmt.Sprintf("translation catalog %s: %v", f.Name(), err))
		}
	}
	return b
}

// setLanguage selects the language of tr and trn and the number format of
// formatNumber. pref is a BCP 47 tag such as "en", or "" for the OS locale.
// Text already on screen does not change, so the UI applies a new choice at
// the next start.
func setLanguage(pref string) {
	if pref == "" {
		pref = lang.SystemLocale().LanguageString()
	}
	localizer = i18n.NewLocalizer(bundle, pref)
	printer = message.NewPrinter(language.Make(pref))
}

// languages returns the languages that have a catalog.
func languages() []language.Tag { return bundle.LanguageTags() }

// args holds the values of the {{.Name}} fields of a message.
type args map[string]any

// tr translates msg, which is both the English text and the catalog key.
// {{.Name}} fields in msg are filled in from data. Numbers must be passed
// already formatted with formatNumber.
func tr(msg string, data ...args) string {
	return localize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{ID: msg, Other: msg},
		TemplateData:   firstArgs(data),
	})
}

// trn is tr for a message with plural forms: one is the English singular,
// other the English plural and the catalog key. {{.Count}} is count.
func trn(one, other string, count int, data ...args) string {
	d := args{"Count": count}
	maps.Copy(d, firstArgs(data))
	return localize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{ID: other, One: one, Other: other},
		PluralCount:    count,
		TemplateData:   d,
	})
}

func firstArgs(data []args) args {
	if len(data) == 0 {
		return nil
	}
	return data[0]
}

// localize returns the translation, or the English text when the catalog
// has none (go-i18n reports that as an error but still returns the text).
func localize(c *i18n.LocalizeConfig) string {
	s, err := localizer.Localize(c)
	if s == "" && err != nil {
		return c.DefaultMessage.Other
	}
	return s
}

// formatNumber formats v with decimals digits after the decimal separator,
// in the number format of the UI locale (for example 1,234.5 or 1.234,5).
func formatNumber(v float64, decimals int) string {
	return printer.Sprintf("%.*f", decimals, v)
}
