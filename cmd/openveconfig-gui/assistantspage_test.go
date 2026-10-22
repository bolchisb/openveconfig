package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// Synthetic assistant sources, written for these tests: system assistants
// of one instruction each, a helper with a radio step, an information
// step, a voltage, a confirmation and an error step, one that reads a
// label nobody gives it once its step is answered, and one that stops
// before its first step with a calculation the compiler cannot do, as
// real assistants do for now.
const (
	testStart  = " DATA 0xA1, 0xA2\n"
	testStop   = " DATA 0x55, 0, 0x64\n"
	testHelper = `
Battery .AID Battery, RL, 3, 1
Note .AID Note, E
Level .AID Level, EL, '2B 4400 5800 4800'
Confirm .AID Confirm, C, 2, 1
Refused .AID Refused, E
 .AUE Battery, btype
 .AIP Note, btype*10
 .AUE Level, lvl
 DATA 0x44, lvl, lvl >> 8
 .AUE Confirm, ok
 .IF ok = 0
  .AER Refused
 .ENDIF
 DATA btype
`
	testStopper = `
Begin .AID Begin, R, 2, 0
 .AUE Begin, go
 DATA notGivenByAnyone
`
	// testLinked's first step has a label but no format: it is the two
	// ends of a link the text puts in its places, not a field to fill.
	testLinked = `
hl .SET []+'\IS\'+'\IL:http://example.invalid/doc\'
Intro .AID Intro, E
 .AUE Intro, hl
Begin .AID Begin, R, 2, 0
 .AUE Begin, go
 DATA 7
`
	testEarly = `
 DATA 'text' + 1
Begin .AID Begin, R, 2, 0
 .AUE Begin, go
`
)

// writePackages writes synthetic packages into a new folder and returns
// it: the system assistants, two versions of the helper, the stopper and
// a file that is not a package.
func writePackages(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(file string, id uint16, src, title, text string) {
		p := &vap.Package{Format: 1, ID: id, Members: []vap.Member{{Name: "x.vll", Data: []byte(src)}}}
		if title != "" {
			p.Members = append(p.Members, vap.Member{Name: title + ".en", Data: []byte(text)})
		}
		if err := os.WriteFile(filepath.Join(dir, file), vap.Encode(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("asfStart_0015.vap", 0x0015, testStart, "", "")
	write("asfStop_000E.vap", 0x000E, testStop, "", "")
	write("Helper_0180.vap", 0x0180, testHelper, "Old helper", "")
	write("Helper_0190.vap", 0x0190, testHelper, "Test helper%2Fsynthetic", "#Battery\r\nWhich battery is it?\r\n#Level\r\nThe level to act at.\r\n")
	write("Stopper_0200.vap", 0x0200, testStopper, "Stopper", "#Begin\r\nGo on?\r\n")
	write("Early_0210.vap", 0x0210, testEarly, "Early stop", "")
	write("Linked_0220.vap", 0x0220, testLinked, "Linked",
		"#Intro\r\n\\FS:1\\Read\\FS:0\\ the %0:smanual%1:s.\r\n_\r\n.A line.\r\n#Begin\r\nGo on?\r\n")
	if err := os.WriteFile(filepath.Join(dir, "broken.vap"), []byte("not a package"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assistantsUI returns a UI whose assistant folder is dir, with the
// Assistants page shown and the folder read.
func assistantsUI(t *testing.T, dir string) (*ui, *page) {
	t.Helper()
	a := test.NewTempApp(t)
	a.Preferences().SetString(prefAssistantFolder, dir)
	u := newUI(a)
	pg := u.pages[len(u.pages)-1]
	if pg.title != "Assistants" {
		t.Fatalf("last page is %q", pg.title)
	}
	u.showPage(pg)
	<-u.assistants.ready
	return u, pg
}

// helperImage returns an image with the helper at its defaults.
func helperImage(t *testing.T, dir string) []byte {
	t.Helper()
	c, err := assistant.Load(dir, "en", 0x0190)
	if err != nil {
		t.Fatal(err)
	}
	im, err := c.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	return im.Bytes
}

// TestAssistantsPageInstalled: Assistant Setup lists the file's
// assistants by title, or by ID without their package, with the bytes
// used, and the unit's card says that they are kept when only settings
// are sent.
func TestAssistantsPageInstalled(t *testing.T) {
	dir := writePackages(t)
	img := helperImage(t, dir)
	u, pg := assistantsUI(t, dir)
	if strings.Contains(dialogTexts(u.overview.view), "In the file") {
		t.Error("the assistants are still on the Overview page")
	}
	loadSettingsFile(u, assistantFile(t, img))
	text := dialogTexts(pg.view)
	for _, want := range []string{"Assistant Setup", "Add assistant", "Test helper/synthetic", "0x0190",
		"Used assistants: (15 bytes used, 2,797 bytes free)", "In the unit", "keeps its assistants when only its settings are sent"} {
		if !strings.Contains(text, want) {
			t.Errorf("page does not show %q:\n%s", want, text)
		}
	}
	if it := u.assistants.tabs.Items; len(it) != 2 || it[0].Text != "Assistant Configuration" || it[1].Text != "Assistant Tools" {
		t.Errorf("tabs %v", it)
	}
	if ap := u.assistants; ap.empty.Visible() || len(ap.installed) != 1 {
		t.Errorf("list of %v, empty text shown %v", ap.installed, ap.empty.Visible())
	}
	bars := findObjects(pg.view, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.ProgressBar); return ok })
	if len(bars) != 1 {
		t.Fatalf("%d usage bars", len(bars))
	}
	l, _ := assistant.ParseLayout(img)
	if b := bars[0].(*widget.ProgressBar); b.Value != float64(l.Used) || b.Max != assistant.Budget {
		t.Errorf("bar %v of %v", b.Value, b.Max)
	}

	// Without the packages, the ID names the assistant.
	u2, pg2 := assistantsUI(t, filepath.Join(dir, "missing"))
	loadSettingsFile(u2, assistantFile(t, img))
	if text := dialogTexts(pg2.view); !strings.Contains(text, "Assistant 0x0190") {
		t.Errorf("without packages the page shows:\n%s", text)
	}
	loadSettingsFile(u2, assistantFile(t, simulator.ErasedArea()))
	if ap := u2.assistants; !ap.empty.Visible() || ap.empty.Text != "No assistants" || len(ap.installed) != 0 {
		t.Errorf("an empty area shows %q (shown %v)", ap.empty.Text, ap.empty.Visible())
	}
}

// TestAssistantSetupButtons: the buttons that would change the file's
// assistants are off; Start assistant and Summary act on the selected
// one, with the file's values.
func TestAssistantSetupButtons(t *testing.T) {
	dir := writePackages(t)
	u, pg := assistantsUI(t, dir)
	loadSettingsFile(u, assistantFile(t, helperImage(t, dir)))
	for _, label := range []string{"Save assistant", "Delete assistant", "Load assistant"} {
		if !findButton(t, pg.view, label).Disabled() {
			t.Errorf("%s is enabled", label)
		}
	}
	arrows := findObjects(pg.view, func(o fyne.CanvasObject) bool {
		b, ok := o.(*widget.Button)
		return ok && b.Text == "" && (b.Icon == theme.MoveUpIcon() || b.Icon == theme.MoveDownIcon())
	})
	if len(arrows) != 2 || !arrows[0].(*widget.Button).Disabled() || !arrows[1].(*widget.Button).Disabled() {
		t.Errorf("%d arrows, or one is enabled", len(arrows))
	}
	start, summary := findButton(t, pg.view, "Start assistant"), findButton(t, pg.view, "Summary")
	if !start.Disabled() || !summary.Disabled() {
		t.Error("Start assistant or Summary is enabled with nothing selected")
	}
	u.assistants.list.Select(0)
	if start.Disabled() || summary.Disabled() {
		t.Fatal("Start assistant or Summary is off with an assistant selected")
	}

	test.Tap(summary)
	var dlg fyne.CanvasObject
	waitFor(t, "the summary", func() bool {
		dlg = u.win.Canvas().Overlays().Top()
		return dlg != nil && strings.Contains(dialogTexts(dlg), "Level")
	})
	if text := dialogTexts(dlg); !strings.Contains(text, "Test helper/synthetic") || !strings.Contains(text, "Battery") ||
		!strings.Contains(text, "Choice 2") || !strings.Contains(text, "48.00 V") || strings.Contains(text, "Confirm") {
		t.Errorf("summary shows:\n%s", text)
	}
	test.Tap(findButton(t, dlg, "Close"))

	test.Tap(start)
	waitFor(t, "the assistant", func() bool {
		dlg = u.win.Canvas().Overlays().Top()
		return dlg != nil && strings.Contains(dialogTexts(dlg), "Step 1 · Battery")
	})
	if r := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.RadioGroup); return ok }); len(r) != 1 ||
		r[0].(*widget.RadioGroup).Selected != "Choice 2" {
		t.Error("the step does not offer the file's value")
	}
}

// openAdd opens the dialog Add assistant and returns it.
func openAdd(t *testing.T, u *ui, pg *page) fyne.CanvasObject {
	t.Helper()
	test.Tap(findButton(t, pg.view, "Add assistant"))
	dlg := u.win.Canvas().Overlays().Top()
	if dlg == nil || !strings.Contains(dialogTexts(dlg), "Add assistant") {
		t.Fatal("no Add assistant dialog")
	}
	return dlg
}

// TestAssistantsPageAvailable: Add assistant lists the folder's user
// assistants by title in their newest version, without the system
// assistants, and counts the files that are not packages.
func TestAssistantsPageAvailable(t *testing.T) {
	u, pg := assistantsUI(t, writePackages(t))
	add := openAdd(t, u, pg)
	text := dialogTexts(add)
	for _, want := range []string{"Test helper/synthetic", "ID 0x0190 · 2 versions in the folder",
		"Stopper", "ID 0x0200 · 1 version in the folder", "1 file in this folder is not an assistant package"} {
		if !strings.Contains(text, want) {
			t.Errorf("Add assistant does not show %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"asfStart", "asfStop", "Old helper"} {
		if strings.Contains(text, not) {
			t.Errorf("Add assistant shows %q", not)
		}
	}
	p := u.assistants.picker
	if len(p.shown) != 4 || p.selected != 0 || !findButton(t, add, "Add").Disabled() {
		t.Errorf("%d assistants, %d selected; Add in the wrong state", len(p.shown), p.selected)
	}
	if !strings.Contains(text, "4 assistants") {
		t.Errorf("no count:\n%s", text)
	}
}

// TestAssistantPickerSearch narrows the list by name and by ID, and says
// when nothing matches.
func TestAssistantPickerSearch(t *testing.T) {
	u, pg := assistantsUI(t, writePackages(t))
	add := openAdd(t, u, pg)
	p := u.assistants.picker
	for q, want := range map[string]string{"early": "Early stop", "0x0200": "Stopper", "HELPER": "Test helper/synthetic"} {
		p.search.SetText(q)
		if len(p.shown) != 1 || p.shown[0].title != want || p.selected != 0 {
			t.Errorf("search %q shows %v", q, p.shown)
		}
		if !strings.Contains(dialogTexts(add), "1 of 4 assistants") {
			t.Errorf("search %q: no count", q)
		}
	}
	p.search.SetText("zzz")
	if text := dialogTexts(add); len(p.shown) != 0 || !strings.Contains(text, "No assistant matches “zzz”") || !findButton(t, add, "Add").Disabled() {
		t.Errorf("no match shows:\n%s", text)
	}
	p.search.SetText("")
	if len(p.shown) != 4 {
		t.Errorf("an empty search shows %d", len(p.shown))
	}
}

// previewOf walks through the assistant whose row in Add assistant shows
// title and returns the dialog of its first step.
func previewOf(t *testing.T, u *ui, pg *page, title string) fyne.CanvasObject {
	t.Helper()
	tapAdd(t, u, pg, title)
	var dlg fyne.CanvasObject
	waitFor(t, "the assistant", func() bool {
		dlg = u.win.Canvas().Overlays().Top()
		return dlg != nil && strings.Contains(dialogTexts(dlg), "Step 1")
	})
	return dlg
}

// tapAdd opens Add assistant and taps Add on the row of title. Adding
// wants a file to put the assistant into, so one is opened when the test
// has none.
func tapAdd(t *testing.T, u *ui, pg *page, title string) {
	t.Helper()
	if u.settings.file == nil {
		loadSettingsFile(u, assistantFile(t, simulator.ErasedArea()))
	}
	add := openAdd(t, u, pg)
	p := u.assistants.picker
	for i, a := range p.shown {
		if a.title == title {
			p.list.Select(i)
			test.Tap(findButton(t, add, "Add"))
			return
		}
	}
	t.Fatalf("no row of %s", title)
}

// TestAssistantWizard walks the helper's steps forwards and back, through
// an invalid entry and the error step, to the summary; nothing is written.
func TestAssistantWizard(t *testing.T) {
	dir := writePackages(t)
	u, pg := assistantsUI(t, dir)
	img := helperImage(t, dir)
	sf := assistantFile(t, img)
	loadSettingsFile(u, sf)
	dlg := previewOf(t, u, pg, "Test helper/synthetic")
	next, back := findButton(t, dlg, "Next"), findButton(t, dlg, "Back")
	want := func(parts ...string) {
		t.Helper()
		text := dialogTexts(dlg)
		for _, p := range parts {
			if !strings.Contains(text, p) {
				t.Fatalf("dialog does not show %q:\n%s", p, text)
			}
		}
	}
	radio := func() *widget.RadioGroup {
		r := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.RadioGroup); return ok })
		if len(r) != 1 {
			t.Fatalf("%d radio groups", len(r))
		}
		return r[0].(*widget.RadioGroup)
	}

	want("Step 1 · Battery", "Which battery is it?")
	if r := radio(); len(r.Options) != 3 || r.Selected != "Choice 2" {
		t.Fatalf("radio %v, %q selected", r.Options, r.Selected)
	}
	if !back.Disabled() {
		t.Error("Back on the first step")
	}
	radio().SetSelected("Choice 3")
	test.Tap(next)
	want("Step 2 · Note", "Values shown: 20", "no text for this step")
	test.Tap(next)
	want("Step 3 · Level", "44.00 … 58.00", "V")
	entry := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*numberEntry); return ok })[0].(*numberEntry)
	if entry.Text != "48.00" {
		t.Errorf("default %q", entry.Text)
	}
	entry.SetText("60")
	test.Tap(next)
	want("Step 3 · Level", "Enter a number from 44.00 to 58.00.")
	entry.SetText("50.5")
	test.Tap(next)
	want("Step 4 · Confirm")
	radio().SetSelected("Choice 1")
	test.Tap(next)
	want("Step 5 · Refused", "does not accept the answers")
	if !next.Disabled() {
		t.Error("Next on the error step")
	}
	test.Tap(back)
	want("Step 4 · Confirm")
	radio().SetSelected("Choice 2")
	test.Tap(next)
	// The file of this test already holds this assistant, so the walk ends
	// in a save of its answers rather than in adding it a second time.
	want("Summary", "Battery", "Choice 3", "Level", "50.50 V", "replace the ones the open file holds")
	if strings.Contains(dialogTexts(dlg), "Confirm") {
		t.Error("an unlisted step is in the summary")
	}
	if findButton(t, dlg, "Save in the file").Disabled() {
		t.Error("Save in the file is disabled with a file open")
	}
	test.Tap(back)
	want("Step 4 · Confirm")
	test.Tap(back)
	want("Step 3 · Level")
	test.Tap(next)
	test.Tap(next)
	want("Summary")
	// Cancel is the way out of the summary without adding anything.
	test.Tap(findButton(t, dlg, "Cancel"))
	if top := u.win.Canvas().Overlays().Top(); top == nil || !strings.Contains(dialogTexts(top), "Add assistant") {
		t.Error("leaving the walk does not return to Add assistant")
	}
	if got, _ := sf.unit.AssistantImage(); !bytes.Equal(got, img) || countChanges(u.settings.dev).total() != 0 {
		t.Error("cancelling the walk changed the file")
	}
}

// TestAssistantWizardStop: an assistant that reads a label nobody gives it
// stops with a message, offers no way on, and goes back to its step.
func TestAssistantWizardStop(t *testing.T) {
	u, pg := assistantsUI(t, writePackages(t))
	dlg := previewOf(t, u, pg, "Stopper")
	next := findButton(t, dlg, "Next")
	test.Tap(next)
	text := dialogTexts(dlg)
	for _, want := range []string{"Stopper cannot be configured yet", "needs details of the unit", "known limitation", "Nothing was changed or written", "notGivenByAnyone"} {
		if !strings.Contains(text, want) {
			t.Errorf("stop does not show %q:\n%s", want, text)
		}
	}
	if !next.Disabled() || len(findButtons(dlg, "Add to the file")) != 0 {
		t.Error("the stopped assistant can go on")
	}
	test.Tap(findButton(t, dlg, "Back"))
	if text := dialogTexts(dlg); !strings.Contains(text, "Step 1 · Begin") || next.Disabled() {
		t.Errorf("Back after the stop shows:\n%s", text)
	}

}

// TestAssistantStepMarkup: a step's text shows with its markup worked
// out, and a label with no format descriptor is a value for that text,
// not a field to fill — the step still goes on.
func TestAssistantStepMarkup(t *testing.T) {
	u, pg := assistantsUI(t, writePackages(t))
	dlg := previewOf(t, u, pg, "Linked")
	text := dialogTexts(dlg)
	for _, want := range []string{"Read", "the ", "manual", "A line."} {
		if !strings.Contains(text, want) {
			t.Errorf("step text lacks %q:\n%s", want, text)
		}
	}
	for _, not := range []string{`\FS:1\`, `\IS\`, `\IL:`, "%0:s", "%1:s", "_"} {
		if strings.Contains(text, not) {
			t.Errorf("step text still shows %q:\n%s", not, text)
		}
	}
	if n := len(findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*numberEntry); return ok })); n != 0 {
		t.Errorf("%d input fields on a step that asks nothing", n)
	}
	// The label without a field goes back as it came, so Next works.
	test.Tap(findButton(t, dlg, "Next"))
	if text := dialogTexts(dlg); !strings.Contains(text, "Go on?") {
		t.Errorf("Next did not reach the second step:\n%s", text)
	}
}

// TestAssistantAddToFile: walking an assistant to its summary and
// pressing Add to the file puts it in the open file, and adding one the
// file already holds is refused.
func TestAssistantAddToFile(t *testing.T) {
	dir := writePackages(t)
	u, pg := assistantsUI(t, dir)
	bare, err := assistant.Load(dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := bare.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	sf := assistantFile(t, empty.Bytes)
	loadSettingsFile(u, sf)

	toSummary := func() fyne.CanvasObject {
		t.Helper()
		dlg := previewOf(t, u, pg, "Test helper/synthetic")
		next := findButton(t, dlg, "Next")
		radio := func() *widget.RadioGroup {
			r := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.RadioGroup); return ok })
			return r[0].(*widget.RadioGroup)
		}
		test.Tap(next) // step 1, its default
		test.Tap(next) // step 2, no input
		test.Tap(next) // step 3, its default
		radio().SetSelected("Choice 2")
		test.Tap(next) // step 4
		if text := dialogTexts(dlg); !strings.Contains(text, "Summary") {
			t.Fatalf("did not reach the summary:\n%s", text)
		}
		return dlg
	}

	test.Tap(findButton(t, toSummary(), "Add to the file"))
	waitFor(t, "the assistant to be added", func() bool {
		img, err := sf.unit.AssistantImage()
		if err != nil {
			return false
		}
		l, err := assistant.ParseLayout(img)
		if err != nil || len(l.Users()) != 1 || l.Users()[0] != 0x0190 {
			return false
		}
		// The page follows the file.
		return len(u.assistants.installed) == 1 && u.assistants.installed[0] == 0x0190
	})

	// Walking the same one again saves over the answers it already has,
	// rather than adding a second copy: the summary says so and the
	// dialog's button says Save in the file (#33).
	before, _ := sf.unit.AssistantImage()
	dlg := toSummary()
	if text := dialogTexts(dlg); !strings.Contains(text, "replace the ones the open file holds") {
		t.Fatalf("walking one the file holds: %s", text)
	}
	test.Tap(findButton(t, dlg, "Save in the file"))
	waitFor(t, "the answers to be saved", func() bool {
		top := u.win.Canvas().Overlays().Top()
		return top != nil && strings.Contains(dialogTexts(top), "Assistant saved")
	})
	after, _ := sf.unit.AssistantImage()
	l, err := assistant.ParseLayout(after)
	if err != nil || len(l.Users()) != 1 {
		t.Fatalf("after saving the file holds %v (%v)", l.Users(), err)
	}
	if !bytes.Equal(after, before) {
		t.Error("saving the same answers changed the image")
	}
}

// TestAssistantWizardEarlyStop: an assistant that stops before its first
// step says so, names itself, and offers neither Back nor Next.
func TestAssistantWizardEarlyStop(t *testing.T) {
	u, pg := assistantsUI(t, writePackages(t))
	tapAdd(t, u, pg, "Early stop")
	var dlg fyne.CanvasObject
	waitFor(t, "the preview", func() bool {
		dlg = u.win.Canvas().Overlays().Top()
		return dlg != nil && strings.Contains(dialogTexts(dlg), "Early stop cannot be configured yet")
	})
	if text := dialogTexts(dlg); !strings.Contains(text, "makes a calculation") || !strings.Contains(text, "Nothing was changed or written") {
		t.Errorf("early stop shows:\n%s", text)
	}
	if !findButton(t, dlg, "Next").Disabled() || !findButton(t, dlg, "Back").Disabled() {
		t.Error("the stopped assistant can go on or back")
	}
}

// testChoices is a package with one step of three buttons, written as
// the shipped packages write them.
const testChoices = `
Pick .AID Pick, R, 3, 0
 .AUE Pick, which
 DATA which
`

// TestWizardChoiceTexts: the buttons of a step say what the package's
// dotted lines say, the step's own text keeps only what is above them,
// and the line a package writes under a choice shows under the buttons.
func TestWizardChoiceTexts(t *testing.T) {
	dir := t.TempDir()
	write := func(file string, id uint16, src, title, text string) {
		t.Helper()
		p := &vap.Package{Format: 1, ID: id, Members: []vap.Member{{Name: "x.vll", Data: []byte(src)}}}
		if title != "" {
			p.Members = append(p.Members, vap.Member{Name: title + ".en", Data: []byte(text)})
		}
		if err := os.WriteFile(filepath.Join(dir, file), vap.Encode(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("asfStart_0015.vap", 0x0015, testStart, "", "")
	write("asfStop_000E.vap", 0x000E, testStop, "", "")
	write("Picker_0190.vap", 0x0190, testChoices, "Picker",
		"#Pick\r\nBattery system\r\nPlease select your system\r\n"+
			".Gel or AGM batteries\r\n"+
			"."+`\ABT,FS:4\LiFePo4 with other type BMS\FS:0\`+"\r\n"+
			`\FC:$00606060\(A BMS over CAN bus, or external protection.)`+"\r\n"+
			".Redflow ZCell batteries\r\n;----\r\n")

	u, pg := assistantsUI(t, dir)
	dlg := previewOf(t, u, pg, "Picker")
	r := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.RadioGroup); return ok })
	if len(r) != 1 {
		t.Fatalf("%d radio groups", len(r))
	}
	want := []string{"Gel or AGM batteries", "LiFePo4 with other type BMS", "Redflow ZCell batteries"}
	if got := r[0].(*widget.RadioGroup).Options; !slices.Equal(got, want) {
		t.Errorf("buttons %q, want %q", got, want)
	}
	text := dialogTexts(dlg)
	if !strings.Contains(text, "Please select your system") {
		t.Errorf("the step does not show its own text:\n%s", text)
	}
	// The choices are off the step's text now: they are on the buttons,
	// which this text does not reach.
	if strings.Contains(text, "Redflow ZCell batteries") {
		t.Errorf("a choice is still in the step's text:\n%s", text)
	}
	if !strings.Contains(text, "LiFePo4 with other type BMS: (A BMS over CAN bus, or external protection.)") {
		t.Errorf("the explanation of a choice is not shown under it:\n%s", text)
	}
	if strings.Contains(text, "ABT") || strings.Contains(text, "FC:$") {
		t.Errorf("markup shows as text:\n%s", text)
	}
}

// testEntryStep is a package with one step of two values, written as the
// shipped packages write them.
const testEntryStep = `
Levels .AID Levels, E, '2B 4400 5800 4800', '2B 4400 5800 5000'
 .AUE Levels, low, high
 DATA low, high
`

// TestWizardFieldLabels: a field is named by the words the package
// writes around its place, the places are off the step's text, the
// first line of the section is the step's heading, and no field is left
// to be called "Value 1".
func TestWizardFieldLabels(t *testing.T) {
	dir := t.TempDir()
	write := func(file string, id uint16, src, title, text string) {
		t.Helper()
		p := &vap.Package{Format: 1, ID: id, Members: []vap.Member{{Name: "x.vll", Data: []byte(src)}}}
		if title != "" {
			p.Members = append(p.Members, vap.Member{Name: title + ".en", Data: []byte(text)})
		}
		if err := os.WriteFile(filepath.Join(dir, file), vap.Encode(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("asfStart_0015.vap", 0x0015, testStart, "", "")
	write("asfStop_000E.vap", 0x000E, testStop, "", "")
	write("Levels_0190.vap", 0x0190, testEntryStep, "Levels",
		"#Levels\r\nSustain voltage\r\nSet the two levels.\r\n"+
			".Sustain voltage during the first 24 hours is |0 V.\r\n"+
			"Sustain voltage after 24 hours is |1 V.\r\n;----\r\n")

	u, pg := assistantsUI(t, dir)
	dlg := previewOf(t, u, pg, "Levels")
	text := dialogTexts(dlg)
	for _, want := range []string{
		"Step 1",              // the number alone
		"Sustain voltage",     // the heading
		"Set the two levels.", // the prose under it
		"Sustain voltage during the first 24 hours is", // the name of a field
		"Sustain voltage after 24 hours is",
		"V", "44.00 … 58.00",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the step does not show %q:\n%s", want, text)
		}
	}
	// The full stop that ended the sentence the words came from is not
	// part of the unit beside a field.
	for _, not := range []string{"Value 1", "Value 2", "|0", "|1", "Step 1 ·", "V."} {
		if strings.Contains(text, not) {
			t.Errorf("the step still shows %q:\n%s", not, text)
		}
	}
	entries := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*numberEntry); return ok })
	if len(entries) != 2 {
		t.Fatalf("%d fields, want 2", len(entries))
	}
	if a, b := entries[0].(*numberEntry).Text, entries[1].(*numberEntry).Text; a != "48.00" || b != "50.00" {
		t.Errorf("fields hold %q and %q, want the defaults 48.00 and 50.00", a, b)
	}
}

// TestAssistantSaveInFile: Start assistant on one the file already holds
// offers Save in the file, and saving replaces its answers in place —
// the assistant stays, and the file keeps only one of it.
func TestAssistantSaveInFile(t *testing.T) {
	dir := writePackages(t)
	u, pg := assistantsUI(t, dir)
	bare, err := assistant.Load(dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := bare.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	one, err := assistant.Add(dir, "en", empty.Bytes, 0x0190, nil,
		wizard.Answers{{Source: "Helper", Step: "battery"}: {asm.IntVal(1)}})
	if err != nil {
		t.Fatal(err)
	}
	sf := assistantFile(t, one.Bytes)
	loadSettingsFile(u, sf)
	if len(u.assistants.installed) != 1 {
		t.Fatalf("the file holds %v", u.assistants.installed)
	}

	// Start assistant walks it with the file's values.
	u.assistants.list.Select(0)
	test.Tap(findButton(t, pg.view, "Start assistant"))
	var dlg fyne.CanvasObject
	waitFor(t, "the assistant", func() bool {
		dlg = u.win.Canvas().Overlays().Top()
		return dlg != nil && strings.Contains(dialogTexts(dlg), "Step 1")
	})
	next := findButton(t, dlg, "Next")
	radio := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.RadioGroup); return ok })[0].(*widget.RadioGroup)
	radio.SetSelected("Choice 3")
	test.Tap(next) // step 1
	test.Tap(next) // step 2, no input
	test.Tap(next) // step 3, its default
	test.Tap(next) // step 4, the confirmation
	if text := dialogTexts(dlg); !strings.Contains(text, "Summary") || !strings.Contains(text, "replace the ones the open file holds") {
		t.Fatalf("the summary of an assistant of the file: %s", text)
	}

	test.Tap(findButton(t, dlg, "Save in the file"))
	waitFor(t, "the answers to be saved", func() bool {
		top := u.win.Canvas().Overlays().Top()
		return top != nil && strings.Contains(dialogTexts(top), "Assistant saved")
	})
	img, err := sf.unit.AssistantImage()
	if err != nil {
		t.Fatal(err)
	}
	l, err := assistant.ParseLayout(img)
	if err != nil || len(l.Users()) != 1 || l.Users()[0] != 0x0190 {
		t.Fatalf("after saving the file holds %v (%v)", l.Users(), err)
	}
	if bytes.Equal(img, one.Bytes) {
		t.Fatal("saving did not change the file")
	}
	// The file holds what replacing the answer builds: the third button
	// where it held the second. (The synthetic package gives its buttons
	// no read-back label, so the answer cannot be read back out of the
	// image; the image itself is the check.)
	want, err := assistant.Replace(dir, "en", one.Bytes, 0x0190, assistantLabels(u, dir),
		wizard.Answers{{Source: "Helper", Step: "battery"}: {asm.IntVal(2)}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(img, want.Bytes) {
		t.Error("the saved image is not the one the new answer builds")
	}
}
