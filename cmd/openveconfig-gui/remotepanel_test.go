package main

import (
	"context"
	"encoding/binary"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// panelSession returns a session on sim whose target is device 0 with
// firmware version fw, without the connection steps.
func panelSession(t *testing.T, sim *simulator.Simulator, fw uint32) *session {
	t.Helper()
	s := &session{client: mk2.NewClient(sim.Port(), nil), lock: make(chan struct{}, 1),
		devices: []byte{0}, firmware: map[byte]uint32{0: fw}}
	t.Cleanup(func() { s.close() })
	return s
}

// newPanelSim returns a simulator with one device and no idle 'V' frames.
func newPanelSim() *simulator.Simulator {
	return simulator.New(simulator.Config{VersionInterval: -1})
}

// pollPanel polls once on the test goroutine; the test driver runs the
// UI update there too.
func pollPanel(t *testing.T, p *remotePanel) {
	t.Helper()
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
}

// fastPanelTimers shortens the poll and blink intervals.
func fastPanelTimers(t *testing.T) {
	t.Helper()
	poll, blink := panelPollInterval, blinkInterval
	t.Cleanup(func() { panelPollInterval, blinkInterval = poll, blink })
	panelPollInterval, blinkInterval = 5*time.Millisecond, 5*time.Millisecond
}

// lockedDriver runs fyne.Do under mu. The test driver runs it on the
// calling goroutine, so without the lock the panel's goroutines and the
// test would update the UI at the same time.
type lockedDriver struct {
	fyne.Driver
	mu *sync.Mutex
}

func (d lockedDriver) DoFromGoroutine(f func(), _ bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f()
}

type lockedApp struct {
	fyne.App
	d fyne.Driver
}

func (a lockedApp) Driver() fyne.Driver { return a.d }

// newLockedUI returns a ui whose fyne.Do calls hold the returned function's
// lock; the test runs its own UI calls through that function.
func newLockedUI(t *testing.T) (*ui, func(func())) {
	t.Helper()
	a := test.NewTempApp(t)
	mu := &sync.Mutex{}
	fyne.SetCurrentApp(lockedApp{App: a, d: lockedDriver{Driver: a.Driver(), mu: mu}})
	return newUI(a), func(f func()) {
		mu.Lock()
		defer mu.Unlock()
		f()
	}
}

// countLED makes sim count the 'L' requests and answer them with st.
func countLED(sim *simulator.Simulator, st mk2.LEDStatus) *atomic.Int32 {
	var n atomic.Int32
	sim.Handle(mk2.CmdLED, func(l *simulator.Line, _ mk2.Frame) {
		n.Add(1)
		l.Send(mk2.Frame{Command: mk2.CmdLED, Data: []byte{st.On, st.Blink}})
	})
	return &n
}

// waitFor fails the test if cond does not become true within 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// highlighted returns the switch positions shown as selected.
func highlighted(p *remotePanel) []mk2.Switch {
	var sel []mk2.Switch
	for _, s := range p.switches {
		if s.b.Importance == widget.HighImportance {
			sel = append(sel, s.sw)
		}
	}
	return sel
}

func TestRemotePanelSwitch(t *testing.T) {
	sim := newPanelSim()
	s := panelSession(t, sim, simulator.Synthetic12V230().Version)
	p := newRemotePanel(newTestUI(t), s)
	if len(p.switches) != 4 {
		t.Fatalf("inverter/charger panel has %d switch positions, want 4", len(p.switches))
	}
	pollPanel(t, p)
	if got := highlighted(p); len(got) != 1 || got[0] != mk2.SwitchOn {
		t.Errorf("start: switch shown %v, want [On]", got)
	}

	// The tap reaches the simulator, which reports it back in 'F' 5.
	test.Tap(findButton(t, p.win.Content(), "Charger only"))
	p.cmds.Wait()
	var m mk2.MasterMultiLED
	s.Do(context.Background(), func(ctx context.Context, c *mk2.Client) (err error) {
		m, err = c.MasterMultiLED(ctx)
		return err
	})
	if switchFromRegister(m.SwitchRegister) != mk2.SwitchChargerOnly {
		t.Errorf("simulator switch register = %#x, want charger only", m.SwitchRegister)
	}
	if m.ActualInputLimit != simulator.Synthetic12V230().InputLimit {
		t.Errorf("switch change moved the limit to %d", m.ActualInputLimit)
	}

	// A change made elsewhere shows up at the next poll.
	s.Do(context.Background(), func(ctx context.Context, c *mk2.Client) error {
		return c.SetState(ctx, mk2.State{Switch: mk2.SwitchInverterOnly, Limit: noLimit, Flags: mk2.StateFlagLimitDeciAmps})
	})
	pollPanel(t, p)
	if got := highlighted(p); len(got) != 1 || got[0] != mk2.SwitchInverterOnly {
		t.Errorf("after an outside change: switch shown %v, want [Inverter only]", got)
	}

	// The default simulator lets a panel override the limit.
	if p.limit.Disabled() {
		t.Error("override allowed: slider disabled")
	}
	if p.limitText.Text != "16.0 A" {
		t.Errorf("limit shown %q, want 16.0 A", p.limitText.Text)
	}
}

func TestRemotePanelNoOverride(t *testing.T) {
	prof := simulator.Synthetic12V230()
	prof.ACInputConfig = 0
	sim := simulator.New(simulator.Config{VersionInterval: -1, Devices: []simulator.Device{{Profile: prof}}})
	p := newRemotePanel(newTestUI(t), panelSession(t, sim, prof.Version))
	pollPanel(t, p)
	// The slider is off and the panel says why.
	if !p.limit.Disabled() || !strings.Contains(p.limitNote.Text, "does not let a remote panel override") {
		t.Errorf("no override: slider disabled %v, note %q", p.limit.Disabled(), p.limitNote.Text)
	}
}

func TestRemotePanelChargerStyle(t *testing.T) {
	p := newRemotePanel(newTestUI(t), panelSession(t, newPanelSim(), 1412345))
	if len(p.switches) != 2 || p.switches[0].sw != mk2.SwitchOn || p.switches[1].sw != mk2.SwitchOff {
		t.Errorf("charger panel switch positions = %v, want On, Off", p.switches)
	}
}

// overrideDevice answers 'S' and 'F' 5 like a device that lets a remote
// panel override its input current limit, which the simulator does not
// offer.
type overrideDevice struct {
	mu       sync.Mutex
	sw       mk2.Switch
	min, max uint16
	limit    uint16
	states   []mk2.Frame
}

func (d *overrideDevice) install(sim *simulator.Simulator) {
	sim.Handle(mk2.CmdState, func(l *simulator.Line, f mk2.Frame) {
		d.mu.Lock()
		d.states = append(d.states, f)
		d.sw = mk2.Switch(f.Data[0])
		if v := binary.LittleEndian.Uint16(f.Data[1:]); f.Data[4]&mk2.StateFlagLimitDeciAmps != 0 && v < noLimit {
			d.limit = min(max(v, d.min), d.max)
		}
		d.mu.Unlock()
		l.Send(mk2.Frame{Command: mk2.CmdState})
	})
	sim.Handle(mk2.CmdFrame, func(l *simulator.Line, f mk2.Frame) {
		if f.Data[0] != mk2.FTypeMasterMultiLED {
			return
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		reg := map[mk2.Switch]byte{mk2.SwitchOn: switchRegCharge | switchRegInvert, mk2.SwitchChargerOnly: switchRegCharge, mk2.SwitchInverterOnly: switchRegInvert}[d.sw]
		data := []byte{0, 0, 0, 0, overrideByPanel}
		for _, v := range []uint16{d.min, d.max, d.limit} {
			data = binary.LittleEndian.AppendUint16(data, v)
		}
		l.Send(mk2.Frame{VEBus: true, Command: mk2.FrameMasterMultiLED, Data: append(data, reg)})
	})
}

// lastState returns the last 'S' request.
func (d *overrideDevice) lastState() (mk2.Switch, uint16, byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f := d.states[len(d.states)-1]
	return mk2.Switch(f.Data[0]), binary.LittleEndian.Uint16(f.Data[1:]), f.Data[4]
}

func TestRemotePanelLimit(t *testing.T) {
	sim := newPanelSim()
	dev := &overrideDevice{sw: mk2.SwitchOn, min: 0, max: 500, limit: 160}
	dev.install(sim)
	p := newRemotePanel(newTestUI(t), panelSession(t, sim, simulator.Synthetic12V230().Version))
	pollPanel(t, p)
	if p.limit.Disabled() || p.limit.Value != 160 || p.limitText.Text != "16.0 A" {
		t.Fatalf("override allowed: slider disabled %v, value %v, text %q; want enabled at 16.0 A",
			p.limit.Disabled(), p.limit.Value, p.limitText.Text)
	}
	if p.limit.Min != 1 || p.limit.Max != 500 {
		t.Errorf("slider range %v-%v, want 1-500: a limit of 0 must not be reachable", p.limit.Min, p.limit.Max)
	}

	p.limit.SetValue(250) // as when the user lets go of the slider
	p.cmds.Wait()
	if sw, limit, flags := dev.lastState(); sw != mk2.SwitchOn || limit != 250 || flags != mk2.StateFlagLimitDeciAmps {
		t.Errorf("limit change sent switch %d, limit %d, flags %#x; want On, 250, variant 2", sw, limit, flags)
	}
	pollPanel(t, p)
	if p.limit.Value != 250 || p.limitText.Text != "25.0 A" {
		t.Errorf("after the change: slider %v, text %q; want 25.0 A", p.limit.Value, p.limitText.Text)
	}

	// A switch change repeats the limit set from the panel.
	test.Tap(findButton(t, p.win.Content(), "Off"))
	p.cmds.Wait()
	if sw, limit, _ := dev.lastState(); sw != mk2.SwitchOff || limit != 250 {
		t.Errorf("switch change sent switch %d, limit %d; want Off, 250", sw, limit)
	}
	pollPanel(t, p)
	if got := highlighted(p); len(got) != 1 || got[0] != mk2.SwitchOff {
		t.Errorf("switch shown %v, want [Off]", got)
	}
}

func TestLEDLit(t *testing.T) {
	st := mk2.LEDStatus{On: mk2.LEDMains | mk2.LEDBulk, Blink: mk2.LEDBulk | mk2.LEDFloat}
	tests := []struct {
		bit         byte
		phase, want bool
	}{
		{mk2.LEDMains, true, true}, {mk2.LEDMains, false, true}, // steady
		{mk2.LEDBulk, true, true}, {mk2.LEDBulk, false, false}, // blinking
		{mk2.LEDFloat, true, false}, {mk2.LEDFloat, false, true}, // blinking in anti-phase
		{mk2.LEDAbsorption, true, false}, {mk2.LEDAbsorption, false, false}, // off
	}
	for _, tt := range tests {
		if got := ledLit(st, tt.bit, tt.phase); got != tt.want {
			t.Errorf("ledLit(bit %#x, phase %v) = %v, want %v", tt.bit, tt.phase, got, tt.want)
		}
	}
}

func TestRemotePanelLEDs(t *testing.T) {
	sim := newPanelSim()
	st := mk2.LEDStatus{On: mk2.LEDMains | mk2.LEDBulk | mk2.LEDOverload, Blink: mk2.LEDBulk | mk2.LEDFloat}
	countLED(sim, st)
	p := newRemotePanel(newTestUI(t), panelSession(t, sim, simulator.Synthetic12V230().Version))
	led := map[byte]*panelLED{}
	for _, l := range p.leds {
		led[l.bit] = l
	}
	if len(led) != 8 {
		t.Fatalf("%d LEDs, want 8", len(led))
	}
	for bit, l := range led {
		if l.ind.state != indicatorUnknown {
			t.Errorf("LED %#x before the first poll: state %v, want unknown", bit, l.ind.state)
		}
	}
	// The colour each LED lights in.
	colours := map[byte]fyne.ThemeColorName{
		mk2.LEDMains: colorNameLEDGreen, mk2.LEDInverter: colorNameLEDGreen,
		mk2.LEDBulk: colorNameLEDAmber, mk2.LEDAbsorption: colorNameLEDAmber, mk2.LEDFloat: colorNameLEDAmber,
		mk2.LEDOverload: colorNameLEDRed, mk2.LEDLowBattery: colorNameLEDRed, mk2.LEDTemperature: colorNameLEDRed,
	}
	for bit, l := range led {
		if l.ind.colour != colours[bit] {
			t.Errorf("LED %#x lights in %s, want %s", bit, l.ind.colour, colours[bit])
		}
	}

	pollPanel(t, p)
	// The LEDs lit in each blink phase.
	lit := map[bool][]byte{
		true:  {mk2.LEDMains, mk2.LEDBulk, mk2.LEDOverload},
		false: {mk2.LEDMains, mk2.LEDFloat, mk2.LEDOverload},
	}
	for _, phase := range []bool{true, false} {
		p.phase = phase
		p.renderLEDs()
		for bit, l := range led {
			w := indicatorOff
			if slices.Contains(lit[phase], bit) {
				w = indicatorOn
			}
			if l.ind.state != w {
				t.Errorf("phase %v: LED %#x state %v, want %v", phase, bit, l.ind.state, w)
			}
		}
	}

	// 0x1F/0x1F means the status is not available (§4).
	countLED(sim, mk2.LEDStatus{On: ledUnknown, Blink: ledUnknown})
	pollPanel(t, p)
	for bit, l := range led {
		if l.ind.state != indicatorUnknown {
			t.Errorf("status unknown: LED %#x state %v, want unknown", bit, l.ind.state)
		}
	}
	if !strings.Contains(p.status.Text, "cannot report its LED status") {
		t.Errorf("status unknown: status %q", p.status.Text)
	}
}

func TestRemotePanelPollingStops(t *testing.T) {
	fastPanelTimers(t)
	u, onUI := newLockedUI(t)
	onUI(func() { showRemotePanel(u) })
	if remotePanels[u] != nil {
		t.Fatal("panel opened without a session")
	}

	sim := newPanelSim()
	polls := countLED(sim, mk2.LEDStatus{On: mk2.LEDMains})
	s := panelSession(t, sim, simulator.Synthetic12V230().Version)
	onUI(func() { u.setSession(s) })
	stopped := func(what string) {
		t.Helper()
		time.Sleep(50 * time.Millisecond) // let a poll in flight finish
		n := polls.Load()
		time.Sleep(50 * time.Millisecond)
		if polls.Load() != n {
			t.Errorf("%s: still polling", what)
		}
	}

	// Closing the window stops the polling.
	var p *remotePanel
	onUI(func() {
		showRemotePanel(u)
		p = remotePanels[u]
	})
	waitFor(t, "polls", func() bool { return polls.Load() >= 3 })
	onUI(func() {
		showRemotePanel(u) // brings the open panel forward
		if remotePanels[u] != p {
			t.Error("a second panel opened")
		}
		p.win.Close()
		if remotePanels[u] != nil || p.ctx.Err() == nil {
			t.Error("the closed panel is still registered or running")
		}
	})
	stopped("window closed")

	// Disconnecting closes the panel.
	onUI(func() {
		showRemotePanel(u)
		p = remotePanels[u]
	})
	start := polls.Load()
	waitFor(t, "polls", func() bool { return polls.Load() >= start+3 })
	onUI(func() {
		u.setSession(nil)
		if remotePanels[u] != nil || p.ctx.Err() == nil {
			t.Error("the panel is still open after the disconnect")
		}
	})
	stopped("disconnected")
}

func TestRemotePanelWaitsForSession(t *testing.T) {
	fastPanelTimers(t)
	sim := newPanelSim()
	polls := countLED(sim, mk2.LEDStatus{On: mk2.LEDMains})
	u, onUI := newLockedUI(t)
	s := panelSession(t, sim, simulator.Synthetic12V230().Version)
	onUI(func() { u.setSession(s) })

	// Another op, such as a settings batch, holds the line.
	held, release := make(chan struct{}), make(chan struct{})
	go s.Do(context.Background(), func(context.Context, *mk2.Client) error {
		close(held)
		<-release
		return nil
	})
	<-held
	onUI(func() { showRemotePanel(u) })
	t.Cleanup(func() { onUI(func() { remotePanels[u].win.Close() }) })
	time.Sleep(20 * panelPollInterval)
	if n := polls.Load(); n != 0 {
		t.Errorf("%d polls while the line was held, want 0", n)
	}
	close(release)
	waitFor(t, "a poll after the line was released", func() bool { return polls.Load() > 0 })
}
