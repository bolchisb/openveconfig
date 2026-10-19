package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/simulator"
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
	banner := demoBanner(u)
	u.setSession(fakeSession(true))
	if !strings.Contains(u.win.Title(), "DEMO") || !banner.Visible() {
		t.Errorf("demo session: title %q, banner visible %v; want a DEMO title and the banner", u.win.Title(), banner.Visible())
	}
	u.setSession(fakeSession(false))
	if u.win.Title() != appName || banner.Visible() {
		t.Errorf("real session: title %q, banner visible %v; want %q and no banner", u.win.Title(), banner.Visible(), appName)
	}
	u.setSession(nil)
}

// TestConnectDemoFromUI connects the demo through the connection bar and
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
