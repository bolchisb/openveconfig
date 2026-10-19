package main

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// busScaleRAM are the RAM variables whose scaling the info frames use
// (§6.1): the frame fields "use the same offsets and scales as the
// equivalent values requested with 'W' commands".
var busScaleRAM = []byte{
	ramMainsV, ramMainsI, ramInverterV, ramInverterI, ramBatV, ramBatI, ramInverterPeriod, ramMainsPeriod,
}

// maxPhases is the number of AC phases the info frames can describe (§6.1).
const maxPhases = 4

// busReading is one poll of the VE.Bus monitor.
type busReading struct {
	// info is the scaling of busScaleRAM; a variable the device does not
	// support is missing.
	info   map[byte]mk2.RAMInfo
	dc     mk2.DCInfo
	phases []mk2.ACInfo // L1 first
}

// value scales raw with the info of RAM variable id; ok is false when the
// device does not support id.
func (r busReading) value(id byte, raw uint16) (v float64, ok bool) {
	info, ok := r.info[id]
	return info.Value(raw), ok
}

// dcCurrent scales a DC current field of the DC info frame with the info
// of the battery current (RAM 5). The field is unsigned and 24 bits wide
// (§6.1), so the formula of §7.3.9, Scale * (x + Offset), is applied to it
// here instead of mk2.RAMInfo.Value, which takes a signed 16-bit word for
// this variable.
func (r busReading) dcCurrent(raw uint32) (v float64, ok bool) {
	info, ok := r.info[ramBatI]
	return info.Scale() * (float64(raw) + float64(info.Offset)), ok
}

// busReader reads the info frames of a VE.Bus system.
type busReader struct {
	target byte // device whose RAM variable info gives the scaling
	infos  *ramInfos
}

// read gets the scaling from the target device, then the DC frame and the
// AC frame of each phase. The L1 frame says how many phases there are.
func (b *busReader) read(ctx context.Context, c *mk2.Client) (busReading, error) {
	r := busReading{info: map[byte]mk2.RAMInfo{}}
	if err := c.SetAddress(ctx, b.target); err != nil {
		return r, err
	}
	for _, id := range busScaleRAM {
		info, ok, err := b.infos.get(ctx, c, id)
		if err != nil {
			return r, err
		}
		if ok {
			r.info[id] = info
		}
	}
	var err error
	if r.dc, err = c.DCInfo(ctx); err != nil {
		return r, err
	}
	l1, err := c.ACInfo(ctx, 1)
	if err != nil {
		return r, err
	}
	r.phases = append(r.phases, l1)
	n := 1
	if l1.PhaseInfo >= mk2.PhaseL1Of1 && l1.PhaseInfo <= mk2.PhaseL1Of4 {
		n = int(l1.PhaseInfo-mk2.PhaseL1Of1) + 1
	}
	for phase := 2; phase <= n; phase++ {
		a, err := c.ACInfo(ctx, phase)
		if err != nil {
			return r, err
		}
		r.phases = append(r.phases, a)
	}
	return r, nil
}

// phaseView shows the AC info frame of one phase.
type phaseView struct {
	card                   *widget.Card
	inV, inI, inF, inP     *field
	outV, outI, outF, outP *field
}

func newPhaseView(n int) *phaseView {
	v := &phaseView{
		inV:  newField(tr("Input voltage")),
		inI:  newField(tr("Input current")),
		inF:  newField(tr("Input frequency")),
		inP:  newField(tr("Input power")),
		outV: newField(tr("Output voltage")),
		outI: newField(tr("Output current")),
		outF: newField(tr("Output frequency")),
		outP: newField(tr("Output power")),
	}
	grid := container.NewGridWithColumns(2)
	for _, f := range []*field{v.inV, v.inI, v.inF, v.inP, v.outV, v.outI, v.outF, v.outP} {
		grid.Add(f.name)
		grid.Add(f.value)
	}
	v.card = widget.NewCard(tr("Phase L{{.Phase}}", args{"Phase": n}), "", grid)
	return v
}

// busView is the content of the VE.Bus monitor window.
type busView struct {
	root   *fyne.Container
	status *widget.Label // why the last poll failed
	phases [maxPhases]*phaseView
	dc     *widget.Card

	batV, batI, invI, chgI *field
}

func newBusView() *busView {
	v := &busView{
		status: widget.NewLabel(tr("Reading...")),
		batV:   newField(tr("Voltage")),
		batI:   newField(tr("Net current")),
		invI:   newField(tr("Used by inverting units")),
		chgI:   newField(tr("From charging units")),
	}
	v.status.Wrapping = fyne.TextWrapWord
	row := container.NewHBox()
	for i := range v.phases {
		v.phases[i] = newPhaseView(i + 1)
		v.phases[i].card.Hide()
		row.Add(v.phases[i].card)
	}
	grid := container.NewGridWithColumns(2)
	for _, f := range []*field{v.batV, v.batI, v.invI, v.chgI} {
		grid.Add(f.name)
		grid.Add(f.value)
	}
	v.dc = widget.NewCard(tr("DC"), "", container.NewVBox(grid))
	v.dc.Hide()
	row.Add(v.dc)
	v.root = container.NewVBox(v.status, row)
	return v
}

// show displays one poll. Phases the system does not have are hidden, and
// so are values whose scaling the device does not report.
func (v *busView) show(s *session, r busReading, err error) {
	if err != nil {
		v.status.SetText(s.deviceFailure(context.Background(), err).Error())
		v.status.Show()
		return
	}
	v.status.Hide()
	set := func(f *field, x float64, ok bool, text func(float64) string) {
		if ok {
			f.set(text(x))
		} else {
			f.hide()
		}
	}
	acV := func(x float64) string { return volts(x, 0) }
	acI := func(x float64) string { return amps(x, 1) }
	// The output period is in the DC frame; it is the same for every phase.
	outPeriod, outPeriodOK := r.value(ramInverterPeriod, uint16(r.dc.InverterPeriod))
	for i, pv := range v.phases {
		if i >= len(r.phases) {
			pv.card.Hide()
			continue
		}
		a := r.phases[i]
		inV, inVOK := r.value(ramMainsV, a.MainsVoltage)
		inI, inIOK := r.value(ramMainsI, a.MainsCurrent)
		inI *= float64(a.BFFactor) // the phase total (§6.1)
		inPeriod, inPeriodOK := r.value(ramMainsPeriod, uint16(a.MainsPeriod))
		outV, outVOK := r.value(ramInverterV, a.InverterVoltage)
		outI, outIOK := r.value(ramInverterI, a.InverterCurrent)
		outI *= float64(a.InverterFactor)
		// Some inverter firmware reports the inverter factor as 0 (§6.1
		// note c), so the phase total is not known.
		outIOK = outIOK && a.InverterFactor != 0
		set(pv.inV, inV, inVOK, acV)
		set(pv.inI, inI, inIOK, acI)
		set(pv.inF, inPeriod, inPeriodOK, frequency)
		// Power is computed as voltage times current, in VA.
		set(pv.inP, inV*inI, inVOK && inIOK, voltAmps)
		set(pv.outV, outV, outVOK, acV)
		set(pv.outI, outI, outIOK, acI)
		set(pv.outF, outPeriod, outPeriodOK, frequency)
		set(pv.outP, outV*outI, outVOK && outIOK, voltAmps)
		pv.card.Show()
	}
	batV, batVOK := r.value(ramBatV, r.dc.Voltage)
	inv, invOK := r.dcCurrent(r.dc.CurrentInverting)
	chg, chgOK := r.dcCurrent(r.dc.CurrentCharging)
	set(v.batV, batV, batVOK, func(x float64) string { return volts(x, 2) })
	set(v.batI, chg-inv, chgOK && invOK, acI)
	set(v.invI, inv, invOK, acI)
	set(v.chgI, chg, chgOK, acI)
	v.dc.Show()
}

// busMonitors holds the open VE.Bus monitor window of each main window.
// It is used on the UI goroutine only.
var busMonitors = map[*ui]fyne.Window{}

// showVEBusMonitor opens the VE.Bus monitor: the info frames of each phase
// and the DC side, polled until the window closes or the session ends.
func showVEBusMonitor(u *ui) {
	if w, ok := busMonitors[u]; ok {
		w.RequestFocus()
		return
	}
	s := u.sess
	if s == nil {
		showMessage(u, tr("VE.Bus monitor"), tr("Connect to a device first."))
		return
	}
	w := u.app.NewWindow(tr("VE.Bus monitor"))
	v := newBusView()
	w.SetContent(container.NewPadded(v.root))
	r := &busReader{target: s.target, infos: newRAMInfos()}
	prefs := u.app.Preferences()
	p := startPoller(s, func() time.Duration { return pollInterval(prefs) }, r.read,
		func(b busReading, err error) { v.show(s, b, err) })
	busMonitors[u] = w
	w.SetOnClosed(func() {
		p.stop()
		delete(busMonitors, u)
	})
	// The window belongs to session s: it closes when s does.
	u.onSession(func(*session) {
		if busMonitors[u] == w {
			w.Close()
		}
	})
	w.Show()
}
