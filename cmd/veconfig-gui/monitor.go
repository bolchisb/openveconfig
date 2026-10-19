package main

// Live monitoring: the compact panel on the Overview page and the polling
// shared with the VE.Bus monitor window (vebusmonitor.go). Values are read
// with the RAM variables and info frames of "Interfacing with VE.Bus
// products - MK2 Protocol 3.14" (Â§6.1, Â§7.3.2, Â§7.3.3, Â§7.3.9, Â§7.3.13.1)
// and scaled with the info each device reports (mk2.RAMInfo).

import (
	"context"
	"errors"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// RAM variable IDs used by the monitors (Â§7.3.13.1).
const (
	ramMainsV         = 0  // AC input voltage
	ramMainsI         = 1  // AC input current
	ramInverterV      = 2  // AC output voltage
	ramInverterI      = 3  // AC output current, inverter side
	ramBatV           = 4  // battery voltage
	ramBatI           = 5  // battery current
	ramBatRipple      = 6  // battery ripple voltage (RMS)
	ramInverterPeriod = 7  // output period, time base 0.1 s
	ramMainsPeriod    = 8  // input period, time base 0.1 s
	ramLoadI          = 9  // signed AC load (output) current
	ramVirtualSwitch  = 10 // virtual switch position
	ramIgnoreAC       = 11 // ignore AC input state
	ramRelay          = 12 // multi-functional relay state
)

// panelRAM are the RAM variables the compact panel reads. RAM 13 (state of
// charge) and the split-phase L2 variables (24, 25, 32, 33) are left out;
// see the issue #29 notes in the report.
var panelRAM = []byte{
	ramMainsV, ramMainsI, ramInverterV, ramInverterI, ramBatV, ramBatI, ramBatRipple,
	ramInverterPeriod, ramMainsPeriod, ramLoadI, ramVirtualSwitch, ramIgnoreAC, ramRelay,
}

// prefMonitorInterval is the preference that holds the time between two
// monitor polls, in milliseconds.
const prefMonitorInterval = "monitor.interval"

// monitorIntervals are the polling intervals offered, in milliseconds. The
// first entry above is the default.
var monitorIntervals = []int{500, 1000, 2000, 5000}

// defaultMonitorInterval is the polling interval, in milliseconds, until
// the user chooses one.
const defaultMonitorInterval = 1000

// pollInterval returns the polling interval saved in prefs.
func pollInterval(prefs fyne.Preferences) time.Duration {
	ms := prefs.IntWithFallback(prefMonitorInterval, defaultMonitorInterval)
	if ms <= 0 {
		ms = defaultMonitorInterval
	}
	return time.Duration(ms) * time.Millisecond
}

// poller repeats a read of a session until it is stopped.
type poller struct {
	cancel context.CancelFunc
	// showing is held while show runs. On the UI goroutine it is never
	// contended; Fyne's test driver runs fyne.Do on the calling goroutine,
	// where it keeps stop from returning during a show.
	showing sync.Mutex
	done    chan struct{} // closed when the polling goroutine has ended
}

// stop ends polling. show is not called after stop returns; a read in
// progress is cancelled but not waited for.
func (p *poller) stop() {
	p.cancel()
	p.showing.Lock()
	p.showing.Unlock()
}

// startPoller calls read through s.Do, hands the result to show on the UI
// goroutine, waits interval() and repeats, until stop is called or a read
// fails because the session is closed. Each read is one Do call, so
// polling waits while another op (a settings read or write) holds the
// session, and never runs in the middle of it.
func startPoller[T any](s *session, interval func() time.Duration,
	read func(ctx context.Context, c *mk2.Client) (T, error), show func(v T, err error)) *poller {
	ctx, cancel := context.WithCancel(context.Background())
	p := &poller{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		for {
			var v T
			err := s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
				var err error
				v, err = read(ctx, c)
				return err
			})
			if ctx.Err() != nil || errors.Is(err, mk2.ErrClosed) {
				return
			}
			fyne.Do(func() {
				p.showing.Lock()
				defer p.showing.Unlock()
				if ctx.Err() == nil {
					show(v, err)
				}
			})
			t := time.NewTimer(interval())
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}()
	return p
}

// fatal reports whether err ends a whole poll rather than one value: the
// poll was stopped or the session closed.
func fatal(ctx context.Context, err error) bool {
	return ctx.Err() != nil || errors.Is(err, mk2.ErrClosed)
}

// ramInfos remembers the scaling the target device reported for its RAM
// variables (Â§7.3.9), so that it is asked once per session. It is used by
// one poller goroutine only.
type ramInfos struct {
	info        map[byte]mk2.RAMInfo
	unsupported map[byte]bool
}

func newRAMInfos() *ramInfos {
	return &ramInfos{info: map[byte]mk2.RAMInfo{}, unsupported: map[byte]bool{}}
}

// get returns the info of RAM variable id, asking the device the first
// time. ok is false when the device does not support id (Sc = 0). A failed
// request is returned as err and asked again next time.
func (r *ramInfos) get(ctx context.Context, c *mk2.Client, id byte) (info mk2.RAMInfo, ok bool, err error) {
	if info, ok := r.info[id]; ok {
		return info, true, nil
	}
	if r.unsupported[id] {
		return mk2.RAMInfo{}, false, nil
	}
	info, err = c.RAMVarInfo(ctx, uint16(id))
	switch {
	case errors.Is(err, mk2.ErrNotSupported):
		r.unsupported[id] = true
		return mk2.RAMInfo{}, false, nil
	case err != nil:
		return mk2.RAMInfo{}, false, err
	}
	r.info[id] = info
	return info, true, nil
}

// panelReading is one poll of the compact panel. A RAM variable that is in
// neither values nor failed is not supported by the device.
type panelReading struct {
	values map[byte]float64 // display values (mk2.RAMInfo.Value)
	failed map[byte]bool    // supported, but the read failed
	// state is the device state (Â§7.3.2) when hasState is set.
	state       mk2.DeviceState
	hasState    bool
	stateFailed bool
}

// has reports whether the device supports RAM variable id.
func (r panelReading) has(id byte) bool {
	_, ok := r.values[id]
	return ok || r.failed[id]
}

// panelReader reads the compact panel's values from one device.
type panelReader struct {
	target  byte
	infos   *ramInfos
	noState bool // the device does not know CommandGetSetDeviceState
}

// read selects the target device and reads every panel variable with its
// own request, as the original tool does, then the device state. A value
// that cannot be read is marked failed; the poll goes on with the next.
func (p *panelReader) read(ctx context.Context, c *mk2.Client) (panelReading, error) {
	r := panelReading{values: map[byte]float64{}, failed: map[byte]bool{}}
	if err := c.SetAddress(ctx, p.target); err != nil {
		return r, err
	}
	for _, id := range panelRAM {
		info, ok, err := p.infos.get(ctx, c, id)
		if fatal(ctx, err) {
			return r, err
		}
		if err != nil {
			r.failed[id] = true
			continue
		}
		if !ok {
			continue
		}
		v, err := c.ReadRAMVar(ctx, id)
		switch {
		case fatal(ctx, err):
			return r, err
		case errors.Is(err, mk2.ErrNotSupported):
		case err != nil:
			r.failed[id] = true
		default:
			r.values[id] = info.Value(v[0])
		}
	}
	if p.noState {
		return r, nil
	}
	st, err := c.DeviceState(ctx, mk2.DeviceStateInquire)
	var unknown *mk2.UnknownCommandError
	switch {
	case fatal(ctx, err):
		return r, err
	case errors.As(err, &unknown):
		// Firmware before xxyy122 does not have the command (Â§7.3.2).
		p.noState = true
	case err != nil:
		r.stateFailed = true
	default:
		r.state, r.hasState = st, true
	}
	return r, nil
}

// field is a named value of a monitor view.
type field struct {
	name, value *widget.Label
}

func newField(name string) *field {
	return &field{name: widget.NewLabel(name), value: widget.NewLabel("")}
}

// set shows the field with text.
func (f *field) set(text string) {
	f.value.SetText(text)
	f.name.Show()
	f.value.Show()
}

// hide hides the field: the device does not support its value.
func (f *field) hide() {
	f.name.Hide()
	f.value.Hide()
}

// visible reports whether the field is shown.
func (f *field) visible() bool { return f.value.Visible() }

// fieldGroup is a titled group of fields, shown while any of them is.
type fieldGroup struct {
	box    *fyne.Container
	fields []*field
}

func newFieldGroup(title string, fields ...*field) *fieldGroup {
	grid := container.NewGridWithColumns(2)
	for _, f := range fields {
		grid.Add(f.name)
		grid.Add(f.value)
	}
	heading := widget.NewLabel(title)
	heading.TextStyle.Bold = true
	return &fieldGroup{box: container.NewVBox(heading, grid), fields: fields}
}

// update shows the group if any of its fields is shown.
func (g *fieldGroup) update() {
	for _, f := range g.fields {
		if f.visible() {
			g.box.Show()
			return
		}
	}
	g.box.Hide()
}

// Value texts. Units are SI symbols after the value (ADR 0001, rule 3).
func volts(v float64, decimals int) string {
	return tr("{{.Value}} V", args{"Value": formatNumber(v, decimals)})
}

func amps(v float64, decimals int) string {
	return tr("{{.Value}} A", args{"Value": formatNumber(v, decimals)})
}

func voltAmps(v float64) string {
	return tr("{{.Value}} VA", args{"Value": formatNumber(v, 0)})
}

// frequency returns the frequency for a period variable's display value,
// which has a time base of 0.1 s (Â§7.3.13.1): f = 10 / period.
func frequency(period float64) string {
	if period <= 0 {
		return tr("n/a")
	}
	return tr("{{.Value}} Hz", args{"Value": formatNumber(10/period, 1)})
}

// onOff names the state of a switch or relay variable. A single-bit
// variable is 0 or 1; any other non-zero value is taken as on.
func onOff(v float64) string {
	if v != 0 {
		return tr("on")
	}
	return tr("off")
}

// readError is the value text of a value that could not be read.
func readError() string { return tr("read error") }

// stateText names a device state reported by CommandGetSetDeviceState or
// the AC info frame (Â§7.3.2, Â§6.1), in our own words.
func stateText(s mk2.DeviceState) string {
	switch s.State {
	case 0:
		return tr("Down")
	case 1:
		return tr("Starting up")
	case 2:
		return tr("Off")
	case 3:
		return tr("Slave")
	case 4:
		return tr("Inverting (full)")
	case 5:
		return tr("Inverting (half)")
	case 6:
		return tr("Inverting (AES)")
	case 7:
		return tr("Power assist")
	case 8:
		return tr("Bypass")
	case 9:
		if stage := chargeStage(s.SubState); stage != "" {
			return tr("Charging ({{.Stage}})", args{"Stage": stage})
		}
		return tr("Charging")
	}
	return tr("Unknown state {{.Code}}", args{"Code": int(s.State)})
}

// chargeStage names a charge sub-state (Â§7.3.2), or returns "".
func chargeStage(sub byte) string {
	switch sub {
	case 0:
		return tr("initializing")
	case 1:
		return tr("bulk")
	case 2:
		return tr("absorption")
	case 3:
		return tr("float")
	case 4:
		return tr("storage")
	case 5:
		return tr("repeated absorption")
	case 6:
		return tr("forced absorption")
	case 7:
		return tr("equalise")
	case 8:
		return tr("bulk stopped")
	}
	return ""
}

// panel is the compact monitor panel of the main window.
type panel struct {
	u      *ui
	root   *fyne.Container
	status *widget.Label // why the last poll failed
	groups []*fieldGroup

	mainsV, mainsI, mainsF          *field
	outV, outI, outF                *field
	batV, batRipple, batI           *field
	state, vswitch, ignoreAC, relay *field
	poll                            *poller
	sess                            *session
}

// monitorPanel returns the compact live readings of the Overview page. It
// is hidden while disconnected.
func monitorPanel(u *ui) fyne.CanvasObject { return newPanel(u).root }

func newPanel(u *ui) *panel {
	p := &panel{
		u:         u,
		status:    widget.NewLabel(""),
		mainsV:    newField(tr("Voltage")),
		mainsI:    newField(tr("Current")),
		mainsF:    newField(tr("Frequency")),
		outV:      newField(tr("Voltage")),
		outI:      newField(tr("Current")),
		outF:      newField(tr("Frequency")),
		batV:      newField(tr("Voltage")),
		batRipple: newField(tr("Ripple")),
		batI:      newField(tr("Current")),
		state:     newField(tr("Device state")),
		vswitch:   newField(tr("Virtual switch")),
		ignoreAC:  newField(tr("Ignore AC input")),
		relay:     newField(tr("Relay")),
	}
	p.status.Wrapping = fyne.TextWrapWord
	p.groups = []*fieldGroup{
		newFieldGroup(tr("AC input"), p.mainsV, p.mainsI, p.mainsF),
		newFieldGroup(tr("AC output"), p.outV, p.outI, p.outF),
		newFieldGroup(tr("DC"), p.batV, p.batRipple, p.batI),
		newFieldGroup(tr("Status"), p.state, p.vswitch, p.ignoreAC, p.relay),
	}
	row := container.NewHBox()
	for _, g := range p.groups {
		g.box.Hide()
		row.Add(g.box)
	}
	interval := intervalSelect(u.app.Preferences())
	p.root = container.NewVBox(row, p.status,
		container.NewHBox(widget.NewLabel(tr("Refresh every:")), interval))
	p.status.Hide()
	p.root.Hide()
	u.onSession(p.setSession)
	return p
}

// intervalSelect returns the choice of polling interval, saved in prefs.
func intervalSelect(prefs fyne.Preferences) *widget.Select {
	labels := make([]string, len(monitorIntervals))
	for i, ms := range monitorIntervals {
		labels[i] = tr("{{.Value}} s", args{"Value": formatNumber(float64(ms)/1000, 1)})
	}
	sel := widget.NewSelect(labels, nil)
	for i, ms := range monitorIntervals {
		if time.Duration(ms)*time.Millisecond == pollInterval(prefs) {
			sel.SetSelectedIndex(i)
		}
	}
	sel.OnChanged = func(string) {
		if i := sel.SelectedIndex(); i >= 0 {
			prefs.SetInt(prefMonitorInterval, monitorIntervals[i])
		}
	}
	return sel
}

// setSession stops polling the old session and starts on s, or hides the
// panel when s is nil.
func (p *panel) setSession(s *session) {
	if p.poll != nil {
		p.poll.stop()
		p.poll = nil
	}
	p.sess = s
	if s == nil {
		p.root.Hide()
		return
	}
	for _, g := range p.groups {
		g.box.Hide()
	}
	p.status.Hide()
	p.root.Show()
	r := &panelReader{target: s.target, infos: newRAMInfos()}
	prefs := p.u.app.Preferences()
	p.poll = startPoller(s, func() time.Duration { return pollInterval(prefs) }, r.read, p.show)
}

// show displays one poll. Values the device does not support are hidden.
func (p *panel) show(r panelReading, err error) {
	if err != nil {
		p.status.SetText(p.sess.deviceFailure(context.Background(), err).Error())
		p.status.Show()
		for _, g := range p.groups {
			for _, f := range g.fields {
				if f.visible() {
					f.set(readError())
				}
			}
		}
		return
	}
	p.status.Hide()
	ram := func(f *field, id byte, text func(float64) string) {
		v, ok := r.values[id]
		switch {
		case ok:
			f.set(text(v))
		case r.failed[id]:
			f.set(readError())
		default:
			f.hide()
		}
	}
	acV := func(v float64) string { return volts(v, 0) }
	acI := func(v float64) string { return amps(v, 1) }
	ram(p.mainsV, ramMainsV, acV)
	ram(p.mainsI, ramMainsI, acI)
	ram(p.mainsF, ramMainsPeriod, frequency)
	ram(p.outV, ramInverterV, acV)
	// The signed load current (RAM 9) replaces the inverter current
	// (RAM 3) when the device has it; negative load current shows as 0.
	if r.has(ramLoadI) {
		ram(p.outI, ramLoadI, func(v float64) string { return amps(max(v, 0), 1) })
	} else {
		ram(p.outI, ramInverterI, acI)
	}
	ram(p.outF, ramInverterPeriod, frequency)
	ram(p.batV, ramBatV, func(v float64) string { return volts(v, 2) })
	ram(p.batRipple, ramBatRipple, func(v float64) string { return volts(v, 2) })
	ram(p.batI, ramBatI, acI)
	ram(p.vswitch, ramVirtualSwitch, onOff)
	ram(p.ignoreAC, ramIgnoreAC, onOff)
	ram(p.relay, ramRelay, onOff)
	switch {
	case r.hasState:
		p.state.set(stateText(r.state))
	case r.stateFailed:
		p.state.set(readError())
	default:
		p.state.hide()
	}
	for _, g := range p.groups {
		g.update()
	}
}
