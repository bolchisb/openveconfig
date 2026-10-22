package main

// A way to look at the assistant wizard as it is drawn, since a layout
// is hard to judge from the text of its widgets. It walks a real
// assistant and writes one PNG per step. Neither the packages nor a
// settings file are committed (they hold Victron's content and a user's
// configuration), so it is skipped unless all three are given:
//
//	ASSISTANT_PACKAGES  the package folder
//	ASSISTANT_RVSC      a settings file of the unit
//	ASSISTANT_SHOTS     the folder to write the PNGs into

import (
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// shotAssistant is the title of the assistant walked; ESS unless
// ASSISTANT_TITLE names another.
func shotAssistant() string {
	if t := os.Getenv("ASSISTANT_TITLE"); t != "" {
		return t
	}
	return "ESS (Energy Storage System)"
}

func TestAssistantShots(t *testing.T) {
	dir, file, out := os.Getenv("ASSISTANT_PACKAGES"), os.Getenv("ASSISTANT_RVSC"), os.Getenv("ASSISTANT_SHOTS")
	if dir == "" || file == "" || out == "" {
		t.Skip("ASSISTANT_PACKAGES, ASSISTANT_RVSC and ASSISTANT_SHOTS not set")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	u, pg := assistantsUI(t, dir)
	u.win.Resize(fyne.NewSize(1400, 1040))
	sf, err := parseSettingsFile(filepath.Base(file), data)
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)

	dlg := previewOf(t, u, pg, shotAssistant())
	next := findButton(t, dlg, "Next")
	for i := 1; i <= 20; i++ {
		name := "step" + strconv.Itoa(i) + ".png"
		text := dialogTexts(dlg)
		t.Logf("%s: %s", name, strings.ReplaceAll(text[:min(len(text), 80)], "\n", " | "))
		f, err := os.Create(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, u.win.Canvas().Capture())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		// The summary is the last thing to see: there Next becomes Close.
		if next.Disabled() || next.Text == tr("Close") {
			break
		}
		test.Tap(next)
	}
}
