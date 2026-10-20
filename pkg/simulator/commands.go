package simulator

import (
	"encoding/binary"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// RAM variable IDs (§7.3.13.1) that the info frames (§6.1) mirror.
const (
	ramUMainsRMS      = 0
	ramIMainsRMS      = 1
	ramUInverterRMS   = 2
	ramIInverterRMS   = 3
	ramUBat           = 4
	ramIBat           = 5
	ramInverterPeriod = 7
	ramMainsPeriod    = 8
)

// device is the live state of one simulated VE.Bus device.
type device struct {
	addr     byte
	p        Profile
	settings map[uint16]Setting
	values   map[uint16]uint16 // setting values
	ramInfo  map[uint16]RAMVar
	ram      map[uint16]uint16 // RAM variable values
	state    mk2.DeviceState

	// pending is the target of the next CommandWriteData (§7.3.7):
	// mk2.WCmdWriteSetting or mk2.WCmdWriteRAMVar, or 0 for none.
	pending   byte
	pendingID uint16

	snapshot    []uint16 // values taken with 'F' 6
	hasSnapshot bool

	sys  *unit // nil unless the device is a system unit
	area *area // nil unless the device has an assistant area
}

func newDevice(d Device) *device {
	dev := &device{
		addr:     d.Address,
		p:        d.Profile,
		settings: map[uint16]Setting{},
		values:   map[uint16]uint16{},
		ramInfo:  map[uint16]RAMVar{},
		ram:      map[uint16]uint16{},
		state:    d.Profile.State,
	}
	if d.System != nil {
		dev.sys = newUnit(d)
		dev.p.Settings = withExternalBF(d.Profile.Settings, d.System.ExternalBF)
	}
	for _, st := range dev.p.Settings {
		dev.settings[st.ID] = st
		dev.values[st.ID] = st.Default
	}
	for _, r := range d.Profile.RAMVars {
		dev.ramInfo[r.ID] = r
		dev.ram[r.ID] = r.Value
	}
	if d.Assistants != nil {
		dev.area = newArea(*d.Assistants)
		dev.addAssistantRAM(dev.area)
	}
	return dev
}

// sendVersion sends the 'V' frame (§3.4). <Mode> is the address set with
// 'A', or 'B' if none is set; 'W' with Config.NineBit.
func (s *Simulator) sendVersion() {
	mode := s.addr
	switch {
	case s.nineBit:
		mode = mk2.VersionModeRS485
	case mode == mk2.AddressNone:
		mode = mk2.VersionModeNoAddress
	}
	d := binary.LittleEndian.AppendUint32(nil, s.version)
	s.send(mk2.Frame{Command: mk2.CmdVersion, Data: append(d, mode)})
}

func (s *Simulator) cmdVersion(_ *Line, _ mk2.Frame) { s.sendVersion() }

// led returns the master's LED status, or 0x1F/0x1F ("unable to determine",
// §4) on an empty bus.
func (s *Simulator) led() mk2.LEDStatus {
	if m := s.master(); m != nil {
		return m.p.LED
	}
	return mk2.LEDStatus{On: 0x1F, Blink: 0x1F}
}

// cmdLED answers 'L' (§4) without the trailing 0x00 that old MK2s send.
func (s *Simulator) cmdLED(_ *Line, _ mk2.Frame) {
	l := s.led()
	s.send(mk2.Frame{Command: mk2.CmdLED, Data: []byte{l.On, l.Blink}})
}

// cmdState handles 'S' (§5). It stores the switch state and the input
// current limit unless <Flags[4]> (do not send) is set, the append-LED flag
// and, when the optional bytes are present, the Winmon mode. In variant 1, <Panel scale> 0, 1 and 255 have special meanings the spec
// describes ambiguously, so they leave the limit unchanged.
func (s *Simulator) cmdState(_ *Line, req mk2.Frame) {
	d := req.Data
	if len(d) < 5 {
		return
	}
	flags := d[4]
	s.appendLED = flags&mk2.StateFlagAppendLED != 0
	if len(d) >= 7 {
		s.mode = mk2.WinmonLong
		if d[6]&mk2.FlagsExt0WinmonShort != 0 {
			s.mode = mk2.WinmonShort
		}
	}
	if flags&mk2.StateFlagNoSend == 0 {
		if sw := mk2.Switch(d[0]); sw >= mk2.SwitchChargerOnly && sw <= mk2.SwitchOff {
			s.sw = sw
		}
		if flags&mk2.StateFlagLimitDeciAmps != 0 {
			if l := binary.LittleEndian.Uint16(d[1:]); l < 0x8000 {
				s.setLimit(l)
			}
		} else if pot, scale := d[1], d[2]; scale != 0 && scale != 1 && scale != 255 {
			s.setLimit(uint16(int(pot) * int(scale) * 10 / 256))
		}
	}
	s.send(mk2.Frame{Command: mk2.CmdState})
}

// setLimit sets the input current limit, clamped to the master's range
// except for 0, which §5 note 3 gives its own meaning.
func (s *Simulator) setLimit(l uint16) {
	m := s.master()
	if m == nil {
		return
	}
	if l != 0 {
		l = min(max(l, m.p.MinInputLimit), m.p.MaxInputLimit)
	}
	s.limit = l
}

// cmdAddress handles 'A' (§7.1): bit 0 of <Action> sets the address; an
// invalid address resets it to 0xFF. The reply omits the trailing 0x00 of
// old MK2s.
func (s *Simulator) cmdAddress(_ *Line, req mk2.Frame) {
	if len(req.Data) < 2 {
		return
	}
	action, addr := req.Data[0], req.Data[1]
	if action&1 != 0 {
		s.addr = addr
		if addr > mk2.MaxAddress {
			s.addr = mk2.AddressNone
		}
	}
	s.send(mk2.Frame{Command: mk2.CmdAddress, Data: []byte{action, s.addr}})
}

// cmdReset handles 'R' (§3.6): the MK2 restarts with its power-up state and
// sends a 'V' frame. The line speed goes back to 2400 (§3.1). That is an
// assumption: the spec says nothing about speed after 'R', but ve-ctrl-tool
// sends 'R' at 115200 and then talks at 2400, which only works if the MK3
// drops back.
func (s *Simulator) cmdReset(l *Line, _ mk2.Frame) {
	l.SetBaudRate(DefaultBaudRate)
	s.addr = mk2.AddressNone
	s.mode = mk2.WinmonShort
	s.appendLED = false
	s.sendVersion()
}

// cmdBaudRate handles the MK3 'N' when Config.HighSpeed is set: the line
// moves to 115200 and nothing is sent back (see Config.HighSpeed).
func (s *Simulator) cmdBaudRate(l *Line, _ mk2.Frame) {
	l.SetBaudRate(mk2.HighSpeedBaudRate)
}

// cmdFrame handles 'F' (§6). The master answers the DC and MasterMultiLED
// requests; the device at position n-1 in address order answers AC Ln, so
// a bus of k devices looks like a k-phase system (up to 4). Reset (8) is
// accepted and ignored; BOL (9) gets no reply, as on an MK2 (§6.4).
func (s *Simulator) cmdFrame(_ *Line, req mk2.Frame) {
	if len(req.Data) < 1 {
		return
	}
	m := s.master()
	if m == nil {
		return
	}
	switch typ := req.Data[0]; typ {
	case mk2.FTypeDC:
		ibat := int16(m.ram[ramIBat])
		var inv, chg uint32
		if ibat < 0 {
			inv = uint32(-int32(ibat))
		} else {
			chg = uint32(ibat)
		}
		d := []byte{0, 0, 0, 0, mk2.PhaseDCFrame}
		d = binary.LittleEndian.AppendUint16(d, m.ram[ramUBat])
		d = append(d, byte(inv), byte(inv>>8), byte(inv>>16), byte(chg), byte(chg>>8), byte(chg>>16))
		d = append(d, byte(m.ram[ramInverterPeriod]))
		s.send(mk2.Frame{VEBus: true, Command: mk2.FrameInfo, Data: d})
	case 1, 2, 3, 4:
		devs := s.live(time.Now())
		phases := min(len(devs), 4)
		if int(typ) > phases {
			return
		}
		dev := devs[typ-1]
		phase := byte(9) - typ // L2 0x07, L3 0x06, L4 0x05
		if typ == 1 {
			phase = mk2.PhaseL1Of1 + byte(phases-1)
		}
		d := []byte{1, 1, 0, dev.state.State, phase}
		for _, id := range []uint16{ramUMainsRMS, ramIMainsRMS, ramUInverterRMS, ramIInverterRMS} {
			d = binary.LittleEndian.AppendUint16(d, dev.ram[id])
		}
		d = append(d, byte(dev.ram[ramMainsPeriod]))
		s.send(mk2.Frame{VEBus: true, Command: mk2.FrameInfo, Data: d})
	case mk2.FTypeMasterMultiLED:
		// Switch register bits 4 and 5: SwitchCharge and SwitchInvert (§6.2).
		var swreg byte
		switch s.sw {
		case mk2.SwitchChargerOnly:
			swreg = 1 << 4
		case mk2.SwitchInverterOnly:
			swreg = 1 << 5
		case mk2.SwitchOn:
			swreg = 1<<4 | 1<<5
		}
		d := []byte{0, 0, 0, 0, m.p.ACInputConfig}
		for _, v := range []uint16{m.p.MinInputLimit, m.p.MaxInputLimit, s.limit} {
			d = binary.LittleEndian.AppendUint16(d, v)
		}
		s.send(mk2.Frame{VEBus: true, Command: mk2.FrameMasterMultiLED, Data: append(d, swreg)})
	case mk2.FTypeSnapshot:
		ids := req.Data[1:]
		if len(ids) > 6 {
			ids = ids[:6]
		}
		for _, dev := range s.devices {
			dev.snapshot = dev.snapshot[:0]
			for _, id := range ids {
				dev.snapshot = append(dev.snapshot, dev.ram[uint16(id)])
			}
			dev.hasSnapshot = true
		}
	}
}
