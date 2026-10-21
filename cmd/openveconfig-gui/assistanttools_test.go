package main

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant/update"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
)

// updateServer serves a made-up product list that offers package set 7,
// and its bundle of two synthetic packages; with missing set, it answers
// 404 to everything. It becomes assistantServer for the test.
func updateServer(t *testing.T, missing bool) {
	t.Helper()
	pkg := func(id uint16) []byte {
		return vap.Encode(&vap.Package{Format: 1, ID: id, Members: []vap.Member{{Name: "x.vll", Data: []byte(testStart)}}})
	}
	var bundle []byte
	for _, f := range []struct {
		name string
		data []byte
	}{{"First_0301.vap", pkg(0x0301)}, {"Second_0302.vap", pkg(0x0302)}} {
		bundle = binary.LittleEndian.AppendUint16(bundle, uint16(len(f.name)))
		bundle = append(bundle, f.name...)
		bundle = binary.LittleEndian.AppendUint32(bundle, 0)
		bundle = binary.LittleEndian.AppendUint32(bundle, 0x20)
		bundle = binary.LittleEndian.AppendUint32(bundle, uint32(len(f.data)))
		bundle = append(bundle, f.data...)
	}
	info := strings.Repeat("header\r\n", 10) + "9000001\r\n" + update.Product + "\r\n/x/setup.exe\r\n\r\n7 /x/packs.vfp\r\n" + strings.Repeat("\r\n", 5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case missing:
			http.NotFound(w, r)
		case r.URL.Path == update.InfoPath:
			w.Write([]byte(info))
		case r.URL.Path == "/x/packs.vfp":
			w.Write(bundle)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	orig := assistantServer
	t.Cleanup(func() { assistantServer = orig })
	assistantServer = srv.URL
}

// toolsTab returns u's Assistants page with the tab Assistant Tools shown.
func toolsTab(t *testing.T, u *ui, pg *page) fyne.CanvasObject {
	t.Helper()
	u.assistants.tabs.SelectIndex(1)
	return pg.view
}

// waitTop waits for a dialog that shows text and returns it.
func waitTop(t *testing.T, u *ui, text string) fyne.CanvasObject {
	t.Helper()
	var top fyne.CanvasObject
	for end := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		top = u.win.Canvas().Overlays().Top()
		if top != nil && strings.Contains(dialogTexts(top), text) {
			return top
		}
		if time.Now().After(end) {
			shown := "nothing"
			if top != nil {
				shown = dialogTexts(top)
			}
			t.Fatalf("timed out waiting for %q; the window shows:\n%s", text, shown)
		}
	}
}

// TestAssistantDownload downloads a package set into an empty folder,
// then finds it up to date, then repairs it.
func TestAssistantDownload(t *testing.T) {
	updateServer(t, false)
	dir := filepath.Join(t.TempDir(), "Assistants")
	u, pg := assistantsUI(t, dir)
	view := toolsTab(t, u, pg)
	if !strings.Contains(dialogTexts(view), "This folder cannot be read") {
		t.Errorf("a missing folder shows:\n%s", dialogTexts(view))
	}

	test.Tap(findButton(t, view, "Download latest assistants"))
	ask := waitTop(t, u, "Package set 7 is available. Download it into "+dir)
	test.Tap(findButton(t, ask, "Download"))
	done := waitTop(t, u, "Package set 7 is in the folder now: 2 packages.")
	<-u.assistants.ready
	if v, by, err := vap.ReadIndex(dir); err != nil || v != 7 || by != 9000001 {
		t.Errorf("index %d, %d, %v", v, by, err)
	}
	if text := dialogTexts(view); !strings.Contains(text, "2 assistant packages · package set 7") {
		t.Errorf("folder after the download:\n%s", text)
	}
	test.Tap(findButton(t, done, "OK"))

	test.Tap(findButton(t, view, "Download latest assistants"))
	test.Tap(findButton(t, waitTop(t, u, "The folder has the latest assistants: package set 7."), "OK"))

	os.WriteFile(filepath.Join(dir, "First_0301.vap"), []byte("broken"), 0o644)
	u.assistants.setDir(dir)
	<-u.assistants.ready
	test.Tap(findButton(t, view, "Repair assistant installation"))
	ask = waitTop(t, u, "1 file in the folder is not a package that can be read. Repair downloads package set 7 again")
	test.Tap(findButton(t, ask, "Repair"))
	waitTop(t, u, "Package set 7 is in the folder now")
	if _, err := vap.Load(filepath.Join(dir, "First_0301.vap")); err != nil {
		t.Errorf("the broken package was not replaced: %v", err)
	}
}

// TestAssistantDownloadUnreachable: a server that does not answer is said
// to be unreachable, and the folder is not touched.
func TestAssistantDownloadUnreachable(t *testing.T) {
	updateServer(t, true)
	dir := t.TempDir()
	u, pg := assistantsUI(t, dir)
	test.Tap(findButton(t, toolsTab(t, u, pg), "Download latest assistants"))
	waitTop(t, u, "Victron Energy's update server cannot be reached")
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("the folder holds %d files", len(ents))
	}
}

// TestAssistantAutoDownloadOption: the check box keeps its preference,
// which is off at first.
func TestAssistantAutoDownloadOption(t *testing.T) {
	u, pg := assistantsUI(t, t.TempDir())
	checks := findObjects(toolsTab(t, u, pg), func(o fyne.CanvasObject) bool {
		c, ok := o.(*widget.Check)
		return ok && c.Text == "Automatically download assistant updates at program startup"
	})
	if len(checks) != 1 {
		t.Fatalf("%d check boxes", len(checks))
	}
	c := checks[0].(*widget.Check)
	if c.Checked {
		t.Error("the start-up download is on at first")
	}
	test.Tap(c)
	if !u.app.Preferences().Bool(prefAssistantAutoDownload) {
		t.Error("the preference was not set")
	}
}

// TestPrivateAssistants lists the private packages next to the package
// folder and deletes one.
func TestPrivateAssistants(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Assistants")
	os.MkdirAll(dir, 0o755)
	private := privateFolder(dir)
	os.MkdirAll(private, 0o755)
	p := &vap.Package{Format: 1, ID: 0x0300, Members: []vap.Member{{Name: "x.vll", Data: []byte(testStart)}, {Name: "My own.en", Data: nil}}}
	mine := filepath.Join(private, "Mine_0300.vap")
	os.WriteFile(mine, vap.Encode(p), 0o644)

	u, pg := assistantsUI(t, dir)
	test.Tap(findButton(t, toolsTab(t, u, pg), "Manage private assistants"))
	dlg := waitTop(t, u, "Private assistants are your own")
	if text := dialogTexts(dlg); !strings.Contains(text, "My own") || !strings.Contains(text, "Mine_0300.vap · ID 0x0300") {
		t.Fatalf("private assistants:\n%s", text)
	}
	test.Tap(findButton(t, dlg, "Delete"))
	test.Tap(findButton(t, waitTop(t, u, "Delete Mine_0300.vap from the private assistants?"), "Delete"))
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Errorf("the package is still there: %v", err)
	}
	if !strings.Contains(dialogTexts(dlg), "No private assistants") {
		t.Error("the list was not updated")
	}
}

// TestAssistantErrorReport: the report names the folder, the file and its
// assistants.
func TestAssistantErrorReport(t *testing.T) {
	dir := writePackages(t)
	u, pg := assistantsUI(t, dir)
	loadSettingsFile(u, assistantFile(t, helperImage(t, dir)))
	test.Tap(findButton(t, toolsTab(t, u, pg), "Error report"))
	text := dialogTexts(waitTop(t, u, "Nothing is sent anywhere"))
	for _, want := range []string{"Package folder: " + dir, "7 packages; files that cannot be read: 1", "Settings file: backup.rvsc",
		"0x0190 Test helper/synthetic", "Connection: none"} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
}
