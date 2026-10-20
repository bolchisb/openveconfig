package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// sourceFiles parses the non-test Go files of the package.
func sourceFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	return fset, files
}

// stringConst returns the value of a string literal or a "+" concatenation
// of string literals.
func stringConst(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok1 := stringConst(e.X)
		y, ok2 := stringConst(e.Y)
		return x + y, ok1 && ok2
	case *ast.ParenExpr:
		return stringConst(e.X)
	}
	return "", false
}

func readCatalog(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("translations", name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

// TestCatalog checks that every tr and trn call has literal English text,
// that translations/en.json has exactly those messages, and that the other
// catalogs have no message that en.json lacks.
func TestCatalog(t *testing.T) {
	fset, files := sourceFiles(t)
	want := map[string]any{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || (fn.Name != "tr" && fn.Name != "trn") {
				return true
			}
			pos := fset.Position(call.Pos())
			first, ok1 := stringConst(call.Args[0])
			if fn.Name == "tr" {
				if !ok1 {
					t.Errorf("%s: tr needs literal English text as its first argument", pos)
				}
				want[first] = first
				return true
			}
			second, ok2 := stringConst(call.Args[1])
			if !ok1 || !ok2 {
				t.Errorf("%s: trn needs literal English text as its first two arguments", pos)
			}
			want[second] = map[string]any{"one": first, "other": second}
			return true
		})
	}

	en := readCatalog(t, "en.json")
	var missing []string
	for k, v := range want {
		if !reflect.DeepEqual(en[k], v) {
			b, _ := json.Marshal(map[string]any{k: v})
			missing = append(missing, string(b))
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Errorf("translations/en.json lacks or has a different text for:\n%s", strings.Join(missing, "\n"))
	}
	for k := range en {
		if _, ok := want[k]; !ok {
			t.Errorf("translations/en.json has %q, which the code does not use", k)
		}
	}

	others, _ := filepath.Glob(filepath.Join("translations", "*.json"))
	for _, p := range others {
		name := filepath.Base(p)
		if name == "en.json" {
			continue
		}
		for k := range readCatalog(t, name) {
			if _, ok := en[k]; !ok {
				t.Errorf("translations/%s has %q, which en.json lacks", name, k)
			}
		}
	}
}

// uiPackages are the packages whose functions and types take text that is
// shown to the user.
var uiPackages = map[string]bool{"widget": true, "dialog": true, "container": true, "fyne": true, "canvas": true}

// textFields are fields and setters that hold text shown to the user.
var textFields = map[string]bool{
	"Text": true, "Label": true, "Title": true, "PlaceHolder": true,
	"SetText": true, "SetTitle": true, "SetPlaceHolder": true, "SetDismissText": true,
}

// TestNoHardCodedUIStrings fails on a non-empty string literal given
// straight to a UI function, type or text field, which would bypass tr.
func TestNoHardCodedUIStrings(t *testing.T) {
	fset, files := sourceFiles(t)
	check := func(e ast.Expr, what string) {
		if s, ok := stringConst(e); ok && s != "" {
			t.Errorf("%s: %s gets the hard-coded text %q; wrap it in tr", fset.Position(e.Pos()), what, s)
		}
	}
	uiSelector := func(e ast.Expr) (string, bool) {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		if x, ok := sel.X.(*ast.Ident); ok && uiPackages[x.Name] {
			return x.Name + "." + sel.Sel.Name, true
		}
		return sel.Sel.Name, textFields[sel.Sel.Name]
	}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if name, ok := uiSelector(n.Fun); ok {
					for _, a := range n.Args {
						check(a, name)
					}
				}
			case *ast.CompositeLit:
				if name, ok := uiSelector(n.Type); ok {
					for _, el := range n.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							check(kv.Value, fmt.Sprintf("%s{%s}", name, kv.Key))
						}
					}
				}
			case *ast.AssignStmt:
				for i, l := range n.Lhs {
					if sel, ok := l.(*ast.SelectorExpr); ok && textFields[sel.Sel.Name] && i < len(n.Rhs) {
						check(n.Rhs[i], sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
}

func TestTr(t *testing.T) {
	if got := tr("Cannot open {{.Port}}: {{.Err}}", args{"Port": "COM3", "Err": "busy"}); got != "Cannot open COM3: busy" {
		t.Errorf("tr with args = %q", got)
	}
	if got := tr("Not in any catalog"); got != "Not in any catalog" {
		t.Errorf("tr of an unknown message = %q, want the English text", got)
	}
	for n, want := range map[int]string{1: "1 device found:", 2: "2 devices found:", 0: "0 devices found:"} {
		if got := trn("{{.Count}} device found:", "{{.Count}} devices found:", n); got != want {
			t.Errorf("trn(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestLanguageOverride checks that setLanguage selects a catalog regardless
// of the OS locale, with English as the fallback.
func TestLanguageOverride(t *testing.T) {
	orig := bundle
	t.Cleanup(func() { bundle = orig; setLanguage("en") })
	bundle = i18n.NewBundle(language.English)
	bundle.MustAddMessages(language.German,
		&i18n.Message{ID: "Connect", Other: "Verbinden"},
		&i18n.Message{ID: "{{.Count}} devices found:", One: "{{.Count}} Gerät gefunden:", Other: "{{.Count}} Geräte gefunden:"})

	setLanguage("de")
	if got := tr("Connect"); got != "Verbinden" {
		t.Errorf("de: tr(Connect) = %q, want Verbinden", got)
	}
	if got := trn("{{.Count}} device found:", "{{.Count}} devices found:", 1); got != "1 Gerät gefunden:" {
		t.Errorf("de: trn(1) = %q", got)
	}
	if got := tr("Disconnect"); got != "Disconnect" {
		t.Errorf("de: untranslated tr(Disconnect) = %q, want the English text", got)
	}
	setLanguage("en")
	if got := tr("Connect"); got != "Connect" {
		t.Errorf("en: tr(Connect) = %q", got)
	}
}

// templateFields returns the sorted, distinct {{.Name}} fields of a catalog
// value: a string, or a map of plural forms.
func templateFields(v any) []string {
	var texts []string
	switch v := v.(type) {
	case string:
		texts = []string{v}
	case map[string]any:
		for _, s := range v {
			text, _ := s.(string)
			texts = append(texts, text)
		}
	}
	var fields []string
	for _, s := range texts {
		for _, m := range regexp.MustCompile(`\{\{\s*\.(\w+)\s*\}\}`).FindAllStringSubmatch(s, -1) {
			fields = append(fields, m[1])
		}
	}
	slices.Sort(fields)
	return slices.Compact(fields)
}

// TestTranslationsComplete checks that every catalog other than en.json
// translates every message of en.json, with the same template fields and
// every plural form its language needs.
func TestTranslationsComplete(t *testing.T) {
	en := readCatalog(t, "en.json")
	others, _ := filepath.Glob(filepath.Join("translations", "*.json"))
	for _, p := range others {
		name := filepath.Base(p)
		if name == "en.json" {
			continue
		}
		cat := readCatalog(t, name)
		loc := i18n.NewLocalizer(bundle, strings.TrimSuffix(name, ".json"))
		for k, ev := range en {
			v, ok := cat[k]
			if !ok {
				t.Errorf("translations/%s lacks %q", name, k)
				continue
			}
			if _, plural := ev.(map[string]any); plural {
				if _, ok := v.(map[string]any); !ok {
					t.Errorf("translations/%s: %q needs plural forms", name, k)
					continue
				}
				for n := range 200 {
					if _, err := loc.Localize(&i18n.LocalizeConfig{MessageID: k, PluralCount: n, TemplateData: args{"Count": n}}); err != nil {
						t.Errorf("translations/%s: %q with count %d: %v", name, k, n, err)
						break
					}
				}
			} else if _, ok := v.(string); !ok {
				t.Errorf("translations/%s: %q must be a plain string", name, k)
				continue
			}
			if want, got := templateFields(ev), templateFields(v); !slices.Equal(got, want) {
				t.Errorf("translations/%s: %q has the fields %v, want %v", name, k, got, want)
			}
		}
	}
}

// TestRomanian checks the Romanian catalog end to end: plain text, a
// template field, the three Romanian plural forms and the number format.
func TestRomanian(t *testing.T) {
	t.Cleanup(func() { setLanguage("en") })
	setLanguage("ro")
	for _, tt := range []struct{ got, want string }{
		{tr("Charger"), "\u00cenc\u0103rc\u0103tor"},
		{tr("Cannot open {{.Port}}: {{.Err}}", args{"Port": "COM3", "Err": "x"}), "Nu se poate deschide COM3: x"},
		{trn("{{.Count}} setting sent.", "{{.Count}} settings sent.", 1), "1 setare trimis\u0103."},
		{trn("{{.Count}} setting sent.", "{{.Count}} settings sent.", 2), "2 set\u0103ri trimise."},
		{trn("{{.Count}} setting sent.", "{{.Count}} settings sent.", 20), "20 de set\u0103ri trimise."},
		{formatNumber(1234.5, 1), "1.234,5"},
		{tr("{{.Value}} V", args{"Value": formatNumber(14.4, 2)}), "14,40 V"},
	} {
		if tt.got != tt.want {
			t.Errorf("ro: got %q, want %q", tt.got, tt.want)
		}
	}
}

func TestFormatNumber(t *testing.T) {
	t.Cleanup(func() { setLanguage("en") })
	for _, tt := range []struct{ lang, want string }{{"en", "1,234.50"}, {"de", "1.234,50"}, {"fr", "1\u00a0234,50"}} {
		setLanguage(tt.lang)
		if got := formatNumber(1234.5, 2); got != tt.want {
			t.Errorf("%s: formatNumber(1234.5, 2) = %q, want %q", tt.lang, got, tt.want)
		}
	}
}
