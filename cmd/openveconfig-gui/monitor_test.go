package main

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// simSession returns a session on a simulator with devs, without the
// connection steps. The first device is the target.
func simSession(t *testing.T, devs ...simulator.Device) (*session, *simulator.Simulator) {
	t.Helper()
	sim := simulator.New(simulator.Config{VersionInterval: -1, Devices: devs})
	s := &session{client: mk2.NewClient(sim.Port(), nil), lock: make(chan struct{}, 1), devices: []byte{}, target: devs[0].Address}
	for _, d := range devs {
		s.devices = append(s.devices, d.Address)
	}
	t.Cleanup(func() { s.close() })
	return s, sim
}

// readOnce runs read through s.Do.
func readOnce[T any](t *testing.T, s *session, read func(context.Context, *mk2.Client) (T, error)) T {
	t.Helper()
	var v T
	err := s.Do(context.Background(), func(ctx context.Context, c *mk2.Client) error {
		var err error
		v, err = read(ctx, c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// holdSession keeps s busy until the test ends, so that no poll can run.
func holdSession(t *testing.T, s *session) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	go s.Do(context.Background(), func(context.Context, *mk2.Client) error {
		close(started)
		<-release
		return nil
	})
	<-started
	t.Cleanup(func() { close(release) })
}

// setRAM sets raw RAM values of the device at addr.
func setRAM(t *testing.T, sim *simulator.Simulator, addr byte, values map[uint16]uint16) {
	t.Helper()
	for id, v := range values {
		if !sim.SetRAMVar(addr, id, v) {
			t.Fatalf("device %d has no RAM variable %d", addr, id)
		}
	}
}

func i16(v int16) uint16 { return uint16(v) }

// TestPanelValues checks that the panel shows the simulator's RAM values,
// scaled with the info the simulated device reports. The expected numbers
// are worked out from the Synthetic12V230 scaling: Sc 32668 is 1/100,
// Sc 30807 with offset 256 is (x+256)/1961, Sc 31791 is x/977, and the
// switch variables are bits 3, 0 and 5.
func TestPanelValues(t *testing.T) {
	s, sim := simSession(t, simulator.Device{Address: 0, Profile: simulator.Synthetic12V230()})
	setRAM(t, sim, 0, map[uint16]uint16{
		0: 23120, 1: 512, 2: 22980, 3: 95, 9: 734,
		4: 1365, 5: i16(-1250), 6: 7,
		7: 136, 8: 195,
		10: 1 << 3, 11: 0, 12: 1 << 5,
	})
	p := newPanel(newTestUI(t))
	p.sess = s
	r := &panelReader{target: 0, infos: newRAMInfos()}
	p.show(readOnce(t, s, r.read), nil)

	want := map[*field]string{
		p.mainsV:    "231 V",
		p.mainsI:    "5.1 A",
		p.mainsF:    formatNumber(10/(195/977.0), 1) + " Hz",
		p.outV:      "230 V",
		p.outI:      "7.3 A", // RAM 9, not RAM 3
		p.outF:      formatNumber(10/((136+256)/1961.0), 1) + " Hz",
		p.batV:      "13.65 V",
		p.batRipple: "0.07 V",
		p.batI:      "-12.5 A",
		p.vswitch:   "on",
		p.ignoreAC:  "off",
		p.relay:     "on",
		p.state:     "Charging (absorption)",
	}
	for f, w := range want {
		if !f.visible() || f.value.Text != w {
			t.Errorf("%s = %q (visible %v), want %q", f.name.Text, f.value.Text, f.visible(), w)
		}
	}
	if want[p.mainsF] != "50.1 Hz" || want[p.outF] != "50.0 Hz" {
		t.Errorf("frequencies %q, %q; want 50.1 Hz and 50.0 Hz", want[p.mainsF], want[p.outF])
	}
	for _, g := range p.groups {
		if !g.box.Visible() {
			t.Error("a group with values is hidden")
		}
	}

	// Negative load current shows as 0.
	setRAM(t, sim, 0, map[uint16]uint16{9: i16(-300)})
	p.show(readOnce(t, s, r.read), nil)
	if got := p.outI.value.Text; got != "0.0 A" {
		t.Errorf("output current with negative load current = %q, want 0.0 A", got)
	}
}

// TestPanelUnsupported checks that values the device does not support are
// hidden, that the output current falls back to RAM 3 without RAM 9, and
// that a failed read shows the error placeholder.
func TestPanelUnsupported(t *testing.T) {
	prof := simulator.Synthetic12V230()
	prof.RAMVars = slices.DeleteFunc(prof.RAMVars, func(v simulator.RAMVar) bool {
		return v.ID == ramLoadI || v.ID == ramVirtualSwitch || v.ID == ramIgnoreAC || v.ID == ramRelay
	})
	s, sim := simSession(t, simulator.Device{Address: 0, Profile: prof})
	setRAM(t, sim, 0, map[uint16]uint16{3: 1830})
	sim.InjectFault(simulator.FaultRule{Cmd: mk2.CmdWinmon, WCmd: mk2.WCmdGetSetDeviceState, Fault: simulator.Unsupported})
	p := newPanel(newTestUI(t))
	p.sess = s
	r := &panelReader{target: 0, infos: newRAMInfos()}
	p.show(readOnce(t, s, r.read), nil)

	if got := p.outI.value.Text; !p.outI.visible() || got != "18.3 A" {
		t.Errorf("output current = %q, want 18.3 A from RAM 3", got)
	}
	for _, f := range []*field{p.state, p.vswitch, p.ignoreAC, p.relay} {
		if f.visible() {
			t.Errorf("%s is shown, but the device does not support it", f.name.Text)
		}
	}
	if p.groups[3].box.Visible() {
		t.Error("the status group is shown without any value")
	}
	if !r.noState {
		t.Error("an unsupported device state request is asked again")
	}

	// One RAM read gets no reply: that value shows an error, the others
	// are still read.
	sim.InjectFault(simulator.FaultRule{Cmd: mk2.CmdWinmon, WCmd: mk2.WCmdReadRAMVar, Fault: simulator.NoReply, Count: 1})
	p.show(readOnce(t, s, r.read), nil)
	if got := p.mainsV.value.Text; got != "Error during read" {
		t.Errorf("input voltage after a lost reply = %q, want the error placeholder", got)
	}
	if got := p.outI.value.Text; got != "18.3 A" {
		t.Errorf("output current after another value failed = %q, want 18.3 A", got)
	}
}

// TestBusMonitorPhases checks the per-phase view on a simulated
// three-phase system: the simulator answers 'F' n with the n-th device.
func TestBusMonitorPhases(t *testing.T) {
	var devs []simulator.Device
	for a := range byte(3) {
		devs = append(devs, simulator.Device{Address: a, Profile: simulator.Synthetic12V230()})
	}
	s, sim := simSession(t, devs...)
	// Per phase: input voltage and current, output voltage and current.
	phases := [][4]uint16{{23040, 412, 23010, 97}, {23300, 260, 22900, 1000}, {22800, 1210, 23100, 0}}
	for a, ph := range phases {
		setRAM(t, sim, byte(a), map[uint16]uint16{0: ph[0], 1: ph[1], 2: ph[2], 3: ph[3]})
	}
	setRAM(t, sim, 0, map[uint16]uint16{4: 2675, 5: i16(-3010), 7: 136, 8: 195})

	v := newBusView()
	r := &busReader{target: 0, infos: newRAMInfos()}
	v.show(s, readOnce(t, s, r.read), nil)

	for i, pv := range v.phases {
		if i >= len(phases) {
			if pv.card.Visible() {
				t.Errorf("L%d is shown on a three-phase system", i+1)
			}
			continue
		}
		ph := phases[i]
		inV, inI := float64(ph[0])/100, float64(ph[1])/100
		outV, outI := float64(ph[2])/100, float64(ph[3])/100
		want := map[*field]string{
			pv.inV:  formatNumber(inV, 0) + " V",
			pv.inI:  formatNumber(inI, 1) + " A",
			pv.inF:  "50.1 Hz",
			pv.inP:  formatNumber(inV*inI, 0) + " VA",
			pv.outV: formatNumber(outV, 0) + " V",
			pv.outI: formatNumber(outI, 1) + " A",
			pv.outF: "50.0 Hz",
			pv.outP: formatNumber(outV*outI, 0) + " VA",
		}
		if !pv.card.Visible() {
			t.Errorf("L%d is hidden", i+1)
		}
		for f, w := range want {
			if !f.visible() || f.value.Text != w {
				t.Errorf("L%d %s = %q (visible %v), want %q", i+1, f.name.Text, f.value.Text, f.visible(), w)
			}
		}
	}
	// The DC frame: 26.75 V, 30.10 A drawn by inverting units.
	for f, w := range map[*field]string{v.batV: "26.75 V", v.batI: "-30.1 A", v.invI: "30.1 A", v.chgI: "0.0 A"} {
		if f.value.Text != w {
			t.Errorf("DC %s = %q, want %q", f.name.Text, f.value.Text, w)
		}
	}

	t.Run("single phase", func(t *testing.T) {
		s, _ := simSession(t, simulator.Device{Address: 0, Profile: simulator.Synthetic48V120()})
		v := newBusView()
		r := &busReader{target: 0, infos: newRAMInfos()}
		v.show(s, readOnce(t, s, r.read), nil)
		for i, pv := range v.phases {
			if pv.card.Visible() != (i == 0) {
				t.Errorf("L%d visible = %v on a single-phase system", i+1, pv.card.Visible())
			}
		}
		if got := v.phases[0].outV.value.Text; got != "120 V" {
			t.Errorf("L1 output voltage = %q, want 120 V", got)
		}
	})
}

// TestPollingWaitsForSettings checks that polls never run in the middle of
// a long settings batch held in one session.Do, that the batch's reads are
// right, and that polling goes on afterwards and ends on stop.
func TestPollingWaitsForSettings(t *testing.T) {
	test.NewTempApp(t) // fyne.Do needs a driver
	s, _ := simSession(t, simulator.Device{Address: 0, Profile: simulator.Synthetic12V230()})
	var inBatch atomic.Bool
	var polls atomic.Int32
	r := &panelReader{target: 0, infos: newRAMInfos()}
	read := func(ctx context.Context, c *mk2.Client) (panelReading, error) {
		polls.Add(1)
		if inBatch.Load() {
			t.Error("a poll started during the settings batch")
		}
		v, err := r.read(ctx, c)
		if inBatch.Load() {
			t.Error("a poll ran into the settings batch")
		}
		return v, err
	}
	shown := make(chan error, 100)
	p := startPoller(s, func() time.Duration { return time.Millisecond }, read,
		func(_ panelReading, err error) { shown <- err })
	waitPolls := func(n int) {
		t.Helper()
		for range n {
			select {
			case err := <-shown:
				if err != nil {
					t.Fatalf("poll failed: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("no poll within 10 s")
			}
		}
	}
	waitPolls(2)

	err := s.Do(context.Background(), func(ctx context.Context, c *mk2.Client) error {
		inBatch.Store(true)
		defer inBatch.Store(false)
		before := polls.Load()
		for range 5 {
			v, err := c.ReadSetting(ctx, 2)
			if err != nil {
				return err
			}
			if v != 1440 {
				t.Errorf("setting 2 read during polling = %d, want 1440", v)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if polls.Load() != before {
			t.Error("polls ran while the batch held the session")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drain what was shown before the batch, then expect new polls.
	for len(shown) > 0 {
		<-shown
	}
	waitPolls(2)

	p.stop()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("polling did not stop")
	}
	n := polls.Load()
	time.Sleep(50 * time.Millisecond)
	if polls.Load() != n {
		t.Error("polls went on after stop")
	}
}

// TestPanelStopsOnDisconnect checks that the panel polls only while a
// session is open, and that the VE.Bus monitor window closes with it.
func TestPanelStopsOnDisconnect(t *testing.T) {
	u := newTestUI(t)
	p := newPanel(u)
	if p.root.Visible() {
		t.Error("panel shown while disconnected")
	}
	s, _ := simSession(t, simulator.Device{Address: 0, Profile: simulator.Synthetic12V230()})
	// Keep the session busy so that no poll touches the widgets while the
	// test reads them; the pollers wait in session.Do.
	holdSession(t, s)
	u.setSession(s)
	if p.poll == nil || !p.root.Visible() {
		t.Fatal("panel not polling after connect")
	}
	showVEBusMonitor(u)
	if _, ok := busMonitors[u]; !ok {
		t.Fatal("VE.Bus monitor did not open")
	}
	old := p.poll
	u.setSession(nil)
	select {
	case <-old.done:
	case <-time.After(5 * time.Second):
		t.Fatal("panel polling did not stop on disconnect")
	}
	if p.poll != nil || p.root.Visible() {
		t.Error("panel still polling or shown after disconnect")
	}
	if _, ok := busMonitors[u]; ok {
		t.Error("VE.Bus monitor still open after disconnect")
	}
}

func TestPollInterval(t *testing.T) {
	prefs := test.NewTempApp(t).Preferences()
	if got := pollInterval(prefs); got != time.Second {
		t.Errorf("default interval = %v, want 1s", got)
	}
	sel := intervalSelect(prefs)
	sel.SetSelectedIndex(0)
	if got := pollInterval(prefs); got != 500*time.Millisecond {
		t.Errorf("interval after choosing %q = %v, want 500ms", sel.Selected, got)
	}
	if got := intervalSelect(prefs).SelectedIndex(); got != 0 {
		t.Errorf("a new choice shows index %d, want the saved 0", got)
	}
}
