package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

func newTestUI(t *testing.T) *ui {
	t.Helper()
	return newUI(test.NewTempApp(t))
}

// fakeSession returns a session on a simulator, without the connection
// steps.
func fakeSession(demo bool) *session {
	sim := simulator.New(simulator.Config{VersionInterval: -1})
	return &session{demo: demo, client: mk2.NewClient(sim.Port(), nil), lock: make(chan struct{}, 1)}
}

func TestOnSessionListeners(t *testing.T) {
	u := newTestUI(t)
	var a, b []*session
	u.onSession(func(s *session) { a = append(a, s) })
	u.onSession(func(s *session) { b = append(b, s) })

	s := fakeSession(false)
	u.setSession(s)
	u.setSession(s) // no change, no call
	u.setSession(nil)
	for _, got := range [][]*session{a, b} {
		if len(got) != 2 || got[0] != s || got[1] != nil {
			t.Errorf("listener calls = %v, want [%p <nil>]", got, s)
		}
	}
	// The closed session's client is closed.
	if _, err := s.client.Version(context.Background()); !errors.Is(err, mk2.ErrClosed) {
		t.Errorf("Version on the replaced session = %v, want mk2.ErrClosed", err)
	}
}

func TestDemoMarking(t *testing.T) {
	u := newTestUI(t)
	pill := u.header.conn
	u.setSession(fakeSession(true))
	if !strings.Contains(u.win.Title(), "DEMO") || !pill.Visible() || !strings.Contains(pill.text, "Demo") ||
		pill.colour != theme.ColorNameWarning || !pill.solid {
		t.Errorf("demo session: title %q, pill %q (visible %v, colour %s, solid %v); want a DEMO title and a solid warning Demo pill",
			u.win.Title(), pill.text, pill.Visible(), pill.colour, pill.solid)
	}
	u.setSession(fakeSession(false))
	if u.win.Title() != appName || strings.Contains(pill.text, "Demo") || pill.colour == theme.ColorNameWarning {
		t.Errorf("real session: title %q, pill %q (colour %s); want %q and no demo marking", u.win.Title(), pill.text, pill.colour, appName)
	}
	u.setSession(nil)
	if pill.Visible() {
		t.Error("connection pill shown while disconnected")
	}
}

// TestShell checks the start screen, the landing page, the sidebar badges
// and the changes bar for a settings file and for a model read from a
// device.
func TestShell(t *testing.T) {
	u := newTestUI(t)
	if !u.start.Visible() || u.shell.Visible() {
		t.Fatal("the start screen is not shown while nothing is open")
	}
	sf, err := parseSettingsFile("test.rvsc", testFile().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	loadSettingsFile(u, sf)
	if u.start.Visible() || !u.shell.Visible() || u.current != u.general {
		t.Fatalf("after opening a file: start %v, shell %v, page %q; want the General page", u.start.Visible(), u.shell.Visible(), u.current.title)
	}
	if !u.header.file.Visible() || !strings.Contains(u.header.file.text, "test.rvsc") || !strings.Contains(u.header.identity.Text, "ZZ0001TEST") {
		t.Errorf("header: file pill %q, identity %q", u.header.file.text, u.header.identity.Text)
	}
	if u.changes.root.Visible() {
		t.Error("changes bar shown without changes")
	}

	d := u.settings.dev
	if err := setNumber(d, mustEntry(t, d, vebus.SettingID(2)), "14.6"); err != nil { // charger
		t.Fatal(err)
	}
	if err := d.SetFlag(5, true); err != nil { // inverter
		t.Fatal(err)
	}
	u.settings.changed()
	badges := map[string]int{}
	for _, p := range u.pages {
		badges[p.title] = p.changes(u)
	}
	if badges["Charger"] != 1 || badges["Inverter"] != 1 || badges["General"] != 0 || badges["Overview"] != 0 {
		t.Errorf("badges = %v, want 1 on Charger and Inverter", badges)
	}
	b := u.changes
	if !b.root.Visible() || !strings.HasPrefix(b.summary.Text, "2 unsaved changes") || b.primary.Text != "Save settings" || b.primary.Disabled() {
		t.Errorf("changes bar: visible %v, summary %q, primary %q (disabled %v)", b.root.Visible(), b.summary.Text, b.primary.Text, b.primary.Disabled())
	}
	if err := setNumber(d, mustEntry(t, d, vebus.SettingID(3)), "15"); err != nil { // float above absorption
		t.Fatal(err)
	}
	u.settings.changed()
	if !strings.Contains(b.summary.Text, "1 invalid value") || !b.primary.Disabled() || !strings.Contains(b.reason.String(), "invalid") {
		t.Errorf("with an invalid value: summary %q, primary disabled %v, reason %q", b.summary.Text, b.primary.Disabled(), b.reason.String())
	}

	// Discard goes back to the file's values.
	discardChanges(u)
	test.Tap(findButton(t, u.win.Canvas().Overlays().Top(), "Discard changes"))
	if len(d.ChangedIDs()) != 0 || b.root.Visible() {
		t.Errorf("after discarding: changed %v, bar visible %v", d.ChangedIDs(), b.root.Visible())
	}

	// A model read from a device cannot be sent while disconnected.
	dev := vebus.NewDevice(nil)
	dev.AddSetting(2, mk2.SettingInfo{Sc: -100, Min: 800, Max: 1750}, 1440)
	u.settings.load(dev, nil)
	_ = setNumber(dev, mustEntry(t, dev, vebus.SettingID(2)), "14.6")
	u.settings.changed()
	if b.primary.Text != "Send settings" || !b.primary.Disabled() || !strings.Contains(b.reason.String(), "Connect to the device") {
		t.Errorf("device model, disconnected: primary %q (disabled %v), reason %q", b.primary.Text, b.primary.Disabled(), b.reason.String())
	}
}

// TestRecentFiles checks that opened files are remembered, newest first,
// and that files that no longer exist are skipped.
func TestRecentFiles(t *testing.T) {
	u := newTestUI(t)
	dir := t.TempDir()
	var uris []fyne.URI
	for _, name := range []string{"a.rvsc", "b.rvsc", "gone.rvsc"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, testFile().Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		uris = append(uris, storage.NewFileURI(p))
	}
	for _, uri := range uris {
		addRecent(u, uri)
	}
	addRecent(u, uris[0]) // again: moves to the front
	if err := os.Remove(filepath.Join(dir, "gone.rvsc")); err != nil {
		t.Fatal(err)
	}
	u.recentsChanged()
	var names []string
	for _, uri := range recentFiles(u.app.Preferences()) {
		names = append(names, uri.Name())
	}
	if !slices.Equal(names, []string{"a.rvsc", "b.rvsc"}) {
		t.Errorf("recent files = %v, want a.rvsc, b.rvsc", names)
	}
	if n := len(u.menu.recent.ChildMenu.Items); n != 2 {
		t.Errorf("File > Recent files has %d items, want 2", n)
	}

	openRecentFile(u, uris[1])
	if u.settings.file == nil || u.settings.file.name != "b.rvsc" {
		t.Error("opening a recent file did not load it")
	}
}

// TestConnectDemoFromUI connects the demo through the connection dialog and
// disconnects again.
func TestConnectDemoFromUI(t *testing.T) {
	u := newTestUI(t)
	got := make(chan *session, 1)
	u.onSession(func(s *session) { got <- s })
	b := &connBar{u: u}

	b.open(portChoice{demo: true})
	var s *session
	select {
	case s = <-got:
	case <-time.After(30 * time.Second):
		t.Fatal("no session after 30 s")
	}
	if s == nil || u.sess != s || !s.demo || len(s.devices) != 2 {
		t.Fatalf("session = %+v, want the open demo session with 2 devices", s)
	}
	if !u.app.Preferences().Bool(prefLastDemo) {
		t.Error("the demo choice was not remembered")
	}
	u.setSession(nil)
	if s := <-got; s != nil || u.sess != nil {
		t.Errorf("after disconnect: listener got %v, u.sess = %v; want nil", s, u.sess)
	}
}

// TestLanguageMenu checks that Options > Language lists Romanian and that
// choosing it is saved in the preferences.
func TestLanguageMenu(t *testing.T) {
	u := newTestUI(t)
	m := languageMenu(u)
	var ro *fyne.MenuItem
	for _, it := range m.Items {
		if it.Label == "română" {
			ro = it
		}
	}
	if ro == nil {
		t.Fatal("the Language menu does not list română")
	}
	ro.Action()
	if got := u.app.Preferences().String(prefLanguage); got != "ro" || !ro.Checked {
		t.Errorf("after choosing română: preference %q, checked %v; want \"ro\", true", got, ro.Checked)
	}
}

func TestRunWithProgressCancel(t *testing.T) {
	u := newTestUI(t)
	done := make(chan error, 1)
	// The op does not touch the UI before it is cancelled: the test driver
	// runs fyne.Do on the calling goroutine, which would race with the
	// test reading the widget tree.
	runWithProgress(u, "test", func(ctx context.Context, progress func(done, total int)) error {
		<-ctx.Done()
		done <- ctx.Err()
		return ctx.Err()
	})
	test.Tap(findButton(t, u.win.Canvas().Overlays().Top(), "Cancel"))
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("op context error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("op was not cancelled")
	}
}

// findButton returns the button labelled text in the tree under o.
func findButton(t *testing.T, o fyne.CanvasObject, text string) *widget.Button {
	t.Helper()
	var found *widget.Button
	var walk func(o fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch w := o.(type) {
		case *widget.Button:
			if w.Text == text {
				found = w
			}
		case *fyne.Container:
			for _, c := range w.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(w).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	if found == nil {
		t.Fatalf("no %q button", text)
	}
	return found
}
