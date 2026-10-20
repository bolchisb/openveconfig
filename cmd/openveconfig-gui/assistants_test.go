package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/bolchisb/openveconfig/internal/rvsctest"
	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/transfer"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

func TestAreaSummary(t *testing.T) {
	bad := simulator.ProgramArea(900)
	bad[0] ^= 1
	for _, tc := range []struct {
		img  []byte
		want string
	}{
		{simulator.ErasedArea(), "No assistants · 2,812 bytes free"},
		{simulator.ProgramArea(900), "Assistant IDs 8737, 9251, 9765 · 906 bytes used, 1,906 free"},
		{bad, "An assistant area of 2,816 bytes with a wrong checksum"},
		{bytes.Repeat([]byte{0xFF}, 1024), "An assistant area of 1,024 bytes whose layout is not known"},
	} {
		if got := areaSummary(tc.img); !strings.HasPrefix(got, tc.want) {
			t.Errorf("areaSummary = %q, want %q", got, tc.want)
		}
	}
	if got := runningText([]uint16{5, 7}); got != "5 (ESS), 7" {
		t.Errorf("runningText = %q", got)
	}
}

// assistantFile is a settings file of the demo unit (long ID 0x10000001)
// whose assistant area is img, with the info of setting 85 that the
// simulator gives a system unit.
func assistantFile(t *testing.T, img []byte) *settingsFile {
	t.Helper()
	spec := rvsctest.FromProfile(simulator.Synthetic12V230(), 192, 0x10000001, "ZZ0001TEST")
	spec.Info[85] = mk2.SettingInfo{Sc: 1, Max: 1}
	spec.Units[0].Assistant = assistant.Compact(img)
	sf, err := parseSettingsFile("backup.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return sf
}

// assistantSession returns a demo session on a bus with the demo unit
// holding the area img, with short waits.
func assistantSession(t *testing.T, img []byte) (*session, *simulator.Simulator) {
	t.Helper()
	orig := assistantWriteOptions
	t.Cleanup(func() { assistantWriteOptions = orig })
	assistantWriteOptions = transfer.Options{ResetWait: 20 * time.Millisecond, BackWindow: 2 * time.Second}
	d := demoAssistantUnit()
	d.Assistants.Image = img
	s, sim := systemTestSession(t, []simulator.Device{d})
	s.devices, s.target = []byte{0}, 0
	return s, sim
}

// assistantsCardOf returns u's Assistants page, which holds the card of
// the installed assistants.
func assistantsCardOf(t *testing.T, u *ui) fyne.CanvasObject {
	t.Helper()
	pg := u.pages[len(u.pages)-1]
	if pg.title != "Assistants" {
		t.Fatalf("last page is %q", pg.title)
	}
	return pg.view
}

func simArea(t *testing.T, sim *simulator.Simulator) ([]byte, int) {
	t.Helper()
	st, ok := sim.Assistants(0)
	if !ok {
		t.Fatal("no assistant area")
	}
	return st.Image, st.Writes
}

// TestAssistantsCard: the card shows the file's assistants, says that real
// devices cannot be read yet, and reads a simulated unit's assistants on
// their own and into the file.
func TestAssistantsCard(t *testing.T) {
	u := newTestUI(t)
	card := assistantsCardOf(t, u)
	if text := dialogTexts(card); !strings.Contains(text, "No settings file is open.") || !strings.Contains(text, "Not connected.") {
		t.Errorf("empty card shows %q", text)
	}
	sf := assistantFile(t, simulator.ErasedArea())
	loadSettingsFile(u, sf)
	if !strings.Contains(dialogTexts(card), "No assistants") {
		t.Errorf("file row shows %q", dialogTexts(card))
	}

	// A real device: nothing can be read.
	u.sess = settingsSession(t) // no listeners: the monitor poller would race with the test driver
	u.settings.changed()
	if text := dialogTexts(card); !strings.Contains(text, "Not available on real devices yet.") ||
		!strings.Contains(text, "Reading assistants from real devices is not available yet") {
		t.Errorf("real device: card shows %q", text)
	}
	if !findButton(t, card, "Read from unit").Disabled() || !findButton(t, card, "Copy into the file").Disabled() {
		t.Error("reading is possible on a real device")
	}

	prog := simulator.ProgramArea(900)
	s, _ := assistantSession(t, prog)
	u.sess = s
	u.settings.changed()
	if text := dialogTexts(card); !strings.Contains(text, "Not read yet.") || !strings.Contains(text, "Copy into the file reads") {
		t.Errorf("demo: card shows %q", text)
	}
	test.Tap(findButton(t, card, "Read from unit"))
	waitFor(t, "the unit's assistants", func() bool {
		text := dialogTexts(card) // a label's text is in it twice
		return strings.Contains(text, "Unit at address 0. Running: 5 (ESS).") && strings.Count(text, "Assistant 0x2423") == 2
	})

	test.Tap(findButton(t, card, "Copy into the file"))
	waitFor(t, "the save question", func() bool {
		top := u.win.Canvas().Overlays().Top()
		return top != nil && strings.Contains(dialogTexts(top), "are in backup.rvsc now")
	})
	if img, _ := sf.unit.AssistantImage(); !bytes.Equal(img, prog) {
		t.Error("the file does not hold the unit's area")
	}
	if strings.Count(dialogTexts(card), "Assistant 0x2423") != 4 {
		t.Error("the file row does not show the new area")
	}
	findButton(t, u.win.Canvas().Overlays().Top(), "Save settings")
}

// fileSendDialog sends sf, loaded in u, to the unit at address 0 with the
// file's assistants and waits for the confirmation dialog.
func fileSendDialog(t *testing.T, u *ui, s *session, sf *settingsFile) fyne.CanvasObject {
	t.Helper()
	sendFile(u, s, u.settings.dev, sf, 0, false, true)
	var dlg fyne.CanvasObject
	for end := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		dlg = u.win.Canvas().Overlays().Top()
		if dlg != nil && strings.Contains(dialogTexts(dlg), "Assistants") {
			return dlg
		}
		if time.Now().After(end) {
			if dlg != nil {
				t.Fatalf("no send confirmation; the dialog shows:\n%s", dialogTexts(dlg))
			}
			t.Fatal("no send confirmation")
		}
	}
}

// TestSendFileAssistants restores a file with assistants to a simulated
// unit: the confirmation shows both areas and the warning, the result each
// step, and the unit then holds the file's area (#81 acceptance 1).
func TestSendFileAssistants(t *testing.T) {
	u := newTestUI(t)
	prog := simulator.ProgramArea(900)
	s, sim := assistantSession(t, simulator.ErasedArea())
	u.sess = s // no listeners: the monitor poller would race with the test driver
	sf := assistantFile(t, prog)
	loadSettingsFile(u, sf)
	set(t, u.settings.dev, 2, "14.6")

	dlg := fileSendDialog(t, u, s, sf)
	text := dialogTexts(dlg)
	for _, want := range []string{"In the unit: No assistants", "In the file: Assistant IDs 8737, 9251, 9765", "every unit restarts"} {
		if !strings.Contains(text, want) {
			t.Errorf("confirmation does not show %q:\n%s", want, text)
		}
	}
	if _, writes := simArea(t, sim); writes != 0 {
		t.Fatalf("%d block writes before Send", writes)
	}
	send := findButton(t, dlg, "Send")
	if send.Disabled() {
		t.Fatal("Send is disabled")
	}
	test.Tap(send)
	waitFor(t, "the send result", func() bool {
		top := u.win.Canvas().Overlays().Top()
		return top != nil && strings.Contains(dialogTexts(top), "blocks written")
	})
	text = dialogTexts(u.win.Canvas().Overlays().Top())
	for _, want := range []string{"1 setting sent.", "44 blocks written", "Read back and verified", "All units restarted", "Running assistants: 5 (ESS)"} {
		if !strings.Contains(text, want) {
			t.Errorf("result does not show %q:\n%s", want, text)
		}
	}
	if img, _ := simArea(t, sim); !bytes.Equal(img, prog) {
		t.Error("the unit does not hold the file's area")
	}
	if v := deviceValue(t, s, 2); v != 1460 {
		t.Errorf("absorption = %d, want 1460", v)
	}
}

// TestSendFileAssistantsRefused: a file of another unit, and any file on a
// real device, cannot be sent with its assistants; the reason is shown and
// nothing is written.
func TestSendFileAssistantsRefused(t *testing.T) {
	u := newTestUI(t)
	s, sim := assistantSession(t, simulator.ErasedArea())
	u.sess = s
	spec := rvsctest.FromProfile(simulator.Synthetic12V230(), 192, 0x10000009, "ZZ0009TEST")
	spec.Info[85] = mk2.SettingInfo{Sc: 1, Max: 1}
	spec.Units[0].Assistant = assistant.Compact(simulator.ProgramArea(900))
	other, err := parseSettingsFile("other.rvsc", spec.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, other)
	dlg := fileSendDialog(t, u, s, other)
	if !findButton(t, dlg, "Send").Disabled() || !strings.Contains(dialogTexts(dlg), "No unit with long ID 10000009 answered") {
		t.Errorf("another unit's file can be sent:\n%s", dialogTexts(dlg))
	}
	pressKey(u, fyne.KeyEscape)
	if _, writes := simArea(t, sim); writes != 0 {
		t.Errorf("%d block writes", writes)
	}

	// On a real device the gate stops it before anything is sent.
	hw := settingsSession(t)
	sf := assistantFile(t, simulator.ProgramArea(900))
	plan := planAssistants(ctxT(t), hw, sf, 0)
	fileDev, _ := sf.device()
	dev := readModel(t, hw, 0)
	confirmFileSend(u, hw, fileDev, dev, vebus.MatchRVSC(sf.info, sf.unit, dev), false, plan)
	dlg = u.win.Canvas().Overlays().Top()
	text := dialogTexts(dlg)
	if !findButton(t, dlg, "Send").Disabled() || !strings.Contains(text, "Writing assistants to real devices is off") ||
		!strings.Contains(text, "keep the unit's assistants to send only the settings") {
		t.Errorf("real device: the file can be sent with its assistants:\n%s", text)
	}
}

// TestAssistantSteps checks the result lines of the outcomes.
func TestAssistantSteps(t *testing.T) {
	writes := &assistPlan{dry: &transfer.Report{}}
	for _, tc := range []struct {
		plan       *assistPlan
		settingsOK bool
		rep        *transfer.Report
		err        error
		want       []string
	}{
		{nil, true, nil, nil, []string{"The unit's assistants were kept"}},
		{&assistPlan{dry: &transfer.Report{Unchanged: true}}, true, nil, nil, []string{"already holds the file's assistants"}},
		{writes, false, nil, nil, []string{"not written, because not every setting was sent"}},
		{writes, true, &transfer.Report{Writes: 44, RolledBack: true, Reset: true},
			&transfer.Error{Kind: transfer.KindRolledBack, Err: transfer.ErrMismatch},
			[]string{"44 blocks written", "The assistants read back differ from the ones written. Writing the assistants failed. The previous assistants were written back"}},
		{writes, true, &transfer.Report{}, &transfer.Error{Kind: transfer.KindRefused, Err: transfer.ErrNotConfigured},
			[]string{"The unit is not configured for assistants, and how to switch them on is not known yet. Nothing was written."}},
	} {
		text := dialogTexts(assistantSteps(tc.plan, tc.settingsOK, tc.rep, tc.err))
		for _, w := range tc.want {
			if !strings.Contains(text, w) {
				t.Errorf("steps %q do not contain %q", text, w)
			}
		}
	}
}
