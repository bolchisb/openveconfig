package main

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
)

func TestShotPicker(t *testing.T) {
	out := `C:\Users\bolch\AppData\Local\Temp\claude\d--victron-porting\c26283a8-1c4d-4cdd-a19b-b888c73c99e7\scratchpad\shots3`
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	dir := `C:\Users\Public\VE Configure tools\Assistants`
	data, err := os.ReadFile(`C:\Users\bolch\Downloads\CJ_Dezmir_copy_1.rvsc`)
	if err != nil {
		t.Fatal(err)
	}
	shot := func(u *ui, name string) {
		t.Helper()
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, u.win.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
	}
	// With a file open.
	u, pg := assistantsUI(t, dir)
	u.win.Resize(fyne.NewSize(1400, 1040))
	sf, err := parseSettingsFile("CJ_Dezmir_copy_1.rvsc", data)
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	openAdd(t, u, pg)
	shot(u, "picker-with-file")

	// With no file open.
	u2, pg2 := assistantsUI(t, dir)
	u2.win.Resize(fyne.NewSize(1400, 1040))
	openAdd(t, u2, pg2)
	shot(u2, "picker-no-file")
}
