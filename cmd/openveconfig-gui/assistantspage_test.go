package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
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
	testEarly = `
 DATA 'text' OR TRUE
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

// TestAssistantsPageInstalled: the page lists the file's assistants by
// title, or by ID without their package, with the bytes used, and says
// that they are kept when only settings are sent.
func TestAssistantsPageInstalled(t *testing.T) {
	dir := writePackages(t)
	img := helperImage(t, dir)
	u, pg := assistantsUI(t, dir)
	if strings.Contains(dialogTexts(u.overview.view), "In the file") {
		t.Error("the assistants are still on the Overview page")
	}
	loadSettingsFile(u, assistantFile(t, img))
	text := dialogTexts(pg.view)
	for _, want := range []string{"Installed assistants", "Test helper/synthetic", "0x0190", "keeps its assistants when only its settings are sent"} {
		if !strings.Contains(text, want) {
			t.Errorf("page does not show %q:\n%s", want, text)
		}
	}
	bars := findObjects(pg.view, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.ProgressBar); return ok })
	if len(bars) != 1 {
		t.Fatalf("%d usage bars", len(bars))
	}
	l, _ := assistant.ParseLayout(img)
	if b := bars[0].(*widget.ProgressBar); b.Value != float64(l.Used) || b.Max != assistant.Budget ||
		b.TextFormatter() != "15 of 2,812 bytes used · 2,797 free" {
		t.Errorf("bar %v of %v: %q", b.Value, b.Max, b.TextFormatter())
	}

	// Without the packages, the ID names the assistant.
	u2, pg2 := assistantsUI(t, filepath.Join(dir, "missing"))
	loadSettingsFile(u2, assistantFile(t, img))
	if text := dialogTexts(pg2.view); !strings.Contains(text, "Assistant 0x0190") || !strings.Contains(text, "No assistant folder") {
		t.Errorf("without packages the page shows:\n%s", text)
	}
	loadSettingsFile(u2, assistantFile(t, simulator.ErasedArea()))
	if !strings.Contains(dialogTexts(pg2.view), "No assistants") {
		t.Error("an empty area is not shown as such")
	}
}

// TestAssistantsPageAvailable: the folder's user assistants are listed by
// title in their newest version, without the system assistants, and the
// files that are not packages are counted.
func TestAssistantsPageAvailable(t *testing.T) {
	_, pg := assistantsUI(t, writePackages(t))
	text := dialogTexts(pg.view)
	for _, want := range []string{"Available assistants", "never ships them", "Test helper/synthetic", "ID 0x0190 · 2 versions in the folder",
		"Stopper", "ID 0x0200 · 1 version in the folder", "1 file in this folder is not an assistant package"} {
		if !strings.Contains(text, want) {
			t.Errorf("page does not show %q:\n%s", want, text)
		}
	}
	for _, not := range []string{"asfStart", "asfStop", "Old helper"} {
		if strings.Contains(text, not) {
			t.Errorf("page shows %q", not)
		}
	}
	if n := len(findObjects(pg.view, func(o fyne.CanvasObject) bool { b, ok := o.(*widget.Button); return ok && b.Text == "Preview…" })); n != 3 {
		t.Errorf("%d preview buttons", n)
	}
}

// previewOf opens the preview of the assistant whose row shows title and
// returns the dialog.
func previewOf(t *testing.T, u *ui, pg *page, title string) fyne.CanvasObject {
	t.Helper()
	rows := findObjects(pg.view, func(o fyne.CanvasObject) bool {
		c, ok := o.(*fyne.Container)
		return ok && len(findButtons(c, "Preview…")) == 1 && strings.Contains(dialogTexts(c), title)
	})
	if len(rows) == 0 {
		t.Fatalf("no row of %s", title)
	}
	test.Tap(findButtons(rows[len(rows)-1], "Preview…")[0])
	var dlg fyne.CanvasObject
	waitFor(t, "the preview", func() bool {
		dlg = u.win.Canvas().Overlays().Top()
		return dlg != nil && strings.Contains(dialogTexts(dlg), "Step 1")
	})
	return dlg
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
	entry := findObjects(dlg, func(o fyne.CanvasObject) bool { _, ok := o.(*widget.Entry); return ok })[0].(*widget.Entry)
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
	want("Summary", "Battery", "Choice 3", "Level", "50.50 V", "Nothing was written")
	if strings.Contains(dialogTexts(dlg), "Confirm") {
		t.Error("an unlisted step is in the summary")
	}
	if !findButton(t, dlg, "Add to the file").Disabled() {
		t.Error("Add to the file is enabled")
	}
	test.Tap(back)
	want("Step 4 · Confirm")
	test.Tap(back)
	want("Step 3 · Level")
	test.Tap(next)
	test.Tap(next)
	want("Summary")
	test.Tap(findButton(t, dlg, "Close"))
	if top := u.win.Canvas().Overlays().Top(); top != nil {
		t.Error("the dialog is still open")
	}
	if got, _ := sf.unit.AssistantImage(); !bytes.Equal(got, img) || countChanges(u.settings.dev).total() != 0 {
		t.Error("the preview changed the file")
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

// TestAssistantWizardEarlyStop: an assistant that stops before its first
// step says so, names itself, and offers neither Back nor Next.
func TestAssistantWizardEarlyStop(t *testing.T) {
	u, pg := assistantsUI(t, writePackages(t))
	rows := findObjects(pg.view, func(o fyne.CanvasObject) bool {
		c, ok := o.(*fyne.Container)
		return ok && len(findButtons(c, "Preview…")) == 1 && strings.Contains(dialogTexts(c), "Early stop")
	})
	if len(rows) == 0 {
		t.Fatal("no row of Early stop")
	}
	test.Tap(findButtons(rows[len(rows)-1], "Preview…")[0])
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
