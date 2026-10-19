# ADR 0001: Localization of the GUI

- Status: accepted; English and Romanian shipped
- Issue: #30
- Date: 2026-10-19

## Context

`veconfig-gui` must be translatable from the start (#30): every UI string goes through one mechanism, English ships first, contributors add languages, the user picks the language in the settings with the OS locale as the default, and numbers are formatted per locale. Translations are written by contributors; Victron's catalogs must never be copied (see [CLEAN-ROOM.md](CLEAN-ROOM.md)).

Two options were considered: Fyne's built-in `fyne.io/fyne/v2/lang`, or a gettext-style catalog.

What Fyne's `lang` (v2.8.2) offers, checked in its source:

- `lang.L(text)` uses the English text as the key; `lang.X(key, fallback)` takes an explicit ID; `lang.N`/`lang.XN` handle plural forms. Messages are Go templates (`{{.Name}}`).
- Catalogs are go-i18n JSON files (`<tag>.json`), added with `lang.AddTranslationsFS`.
- The language is chosen only from the OS locale (`github.com/jeandeaual/go-locale`). The localizer is private, so an app cannot select another language. On Linux the locale comes from `LANGUAGE`/`LC_ALL`/`LANG`, but on Windows it comes from `GetUserDefaultLocaleName`, and on macOS from the system preferences, so no environment variable can override it there.

So `lang` cannot meet "language selection in settings" on Windows and macOS.

## Decision

Use [go-i18n](https://github.com/nicksnyder/go-i18n) v2 (MIT), the library under Fyne's `lang`, directly, through a small wrapper in `cmd/veconfig-gui/i18n.go`:

- `tr("English text {{.Name}}", args{"Name": v})` translates a message. The English text is the key, as with `lang.L`.
- `trn("{{.Count}} item", "{{.Count}} items", n)` handles plural forms. The plural English text is the key.
- `formatNumber(v, decimals)` formats a number for the UI locale with `golang.org/x/text/message` (for example `1,234.5` in English and `1.234,5` in German).
- `setLanguage(tag)` picks the catalog and number format; `""` means the OS locale (read with Fyne's `lang.SystemLocale`). Languages without a catalog fall back to English.

Catalogs are embedded from `cmd/veconfig-gui/translations/<tag>.json`, in the same JSON format Fyne uses. The language is chosen in Options > Language and saved in the Fyne preferences; it takes effect at the next start.

## Consequences

- One mechanism for all our strings, with plural forms and a language choice that works on every platform.
- The catalog format is the same as Fyne's, so moving to `lang` later, if Fyne adds a language override, is a mechanical change.
- The few strings that Fyne draws itself (for example the context menu of text fields, or the file dialog) follow the OS locale, not the language chosen in the app. Our own dialogs set their button texts through `tr`.
- A language change needs a restart. The UI builds its text once.
- go-i18n and `golang.org/x/text` were already linked into the binary by Fyne; they are now direct dependencies in `go.mod`.
- Romanian (`ro.json`) is the second language and the end-to-end proof: `TestRomanian` checks plain text, template fields, the three Romanian plural forms and the Romanian number format.
- The checks below are ordinary tests, so the `go test ./...` step of the `test` job in `.github/workflows/ci.yml` runs them on Linux, macOS and Windows. A hard-coded UI string, a message missing from `en.json` or an incomplete translation fails CI; no separate job is needed.

## Rules for contributors

1. Every user-visible string goes through `tr` or `trn`, including error messages shown to the user. Product and protocol names that are not translated (the app name, "VE.Bus", "MK3") are constants or appear inside translated text.
2. The first argument of `tr` (the first two of `trn`) is literal English text, so that the catalog can be checked. Concatenating literals with `+` is fine. Never build a message from pieces with `+` or `fmt.Sprintf` at run time; use template fields: `tr("Cannot open {{.Port}}", args{"Port": name})`.
3. Pass numbers to templates already formatted with `formatNumber`. Integers that are identifiers, such as firmware versions, addresses or baud rates, are passed with `strconv`, without digit grouping. Units are SI symbols, not translated, written in the message text after the value (`"{{.Value}} V"`), so a translation can change the spacing.
4. Every message must be in `translations/en.json`, with the English text as both key and value (`{"one": ..., "other": ...}` for `trn`). `TestCatalog` fails when a message is missing or unused, and prints the missing entries in JSON. Other catalogs may only contain keys that `en.json` has.
5. Do not use a lone go-i18n reserved word as a message key: `id`, `description`, `hash`, `leftdelim`, `rightdelim`, `zero`, `one`, `two`, `few`, `many`, `other`, `translation` (any case). Make the text more specific instead.
6. `TestNoHardCodedUIStrings` fails when a non-empty string literal is passed directly to a function or type from `widget`, `dialog`, `container`, `canvas` or `fyne`, or to a text field or setter (`Text`, `Label`, `Title`, `PlaceHolder`, `SetText`, `SetTitle`, `SetPlaceHolder`, `SetDismissText`). It cannot catch every case, such as a literal stored in a variable first, so reviewers still check new UI code.
7. To add a language, copy `en.json` to `<tag>.json` (a BCP 47 tag such as `de` or `pt-BR`), translate the values and keep the keys and the `{{.Name}}` fields. A plural message needs every plural form the language has in CLDR (for example `one`, `few` and `other` in Romanian). The language then appears in Options > Language under its own name. Write the translation yourself; never copy it from Victron software.
8. `TestTranslationsComplete` fails when a catalog lacks a message of `en.json`, a translation has other `{{.Name}}` fields than the English text, or a plural form of the language is missing. A new message therefore goes into every catalog; if you cannot translate it, say so in the pull request so that a speaker of the language can.
