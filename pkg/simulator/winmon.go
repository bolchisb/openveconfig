package simulator

import (
	"bytes"
	"encoding/binary"
	"slices"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// cmdWinmon handles the Winmon commands 'W', 'X/Y/Z' and 'x/y/z' (§7.3).
// They go to the device at the address set with 'A', or at the address in
// the frame for 'x/y/z'; with no device there, nothing replies (§7.2).
// Replies use the uppercase tag.
//
// The Winmon mode (set with 'S', §5) changes the reply formats of
// CommandSendSoftwareVersion, CommandGetSettingInfo and
// CommandGetRAMVarInfo, and CommandWriteViaID works in mode 2 only
// (§7.3.11). Requests are not truncated in mode 1: a CommandReadRAMVar
// with several IDs gets all their values, like the "[DD DD]" of newer
// devices in Appendix 1.
func (s *Simulator) cmdWinmon(_ *Line, f mk2.Frame) {
	r, err := mk2.ParseWinmonRequest(f)
	if err != nil {
		return
	}
	addr := s.addr
	switch r.Tag {
	case 'x', 'y', 'z':
		addr = r.Dest
	}
	d := s.device(addr)
	if d == nil {
		return
	}
	reply := func(payloads ...[]byte) {
		for _, p := range payloads {
			s.send(mk2.Frame{Command: mk2.ReplyTag(r.Tag), Data: p})
		}
	}
	unknown := func(reason byte) { reply([]byte{mk2.WReplyUnknownCommand, reason, 0}) }
	if s.fault == AccessLevel {
		reply([]byte{mk2.WReplyAccessLevelRequired, 0, 0})
		return
	}
	unsupported := s.fault == Unsupported
	long := s.mode == mk2.WinmonLong
	// A W frame always has two info bytes (§7.3).
	info := append(append([]byte(nil), r.Info...), 0, 0)
	id := binary.LittleEndian.Uint16(info)

	switch r.Cmd {
	case mk2.WCmdSendAccessPassword: // reverse-engineered (#9); see Config.AccessPasswords
		if slices.Contains(s.passwords, string(r.Info)) {
			s.access = 0xFF
			reply([]byte{mk2.WReplyAccessPasswordOK, 0, 0})
		} else {
			reply([]byte{mk2.WReplyAccessLevelRequired, 0, 0})
		}

	case mk2.WCmdReadGridInfo: // reverse-engineered (#84); see Config.GridInfo
		part, block := int(info[0]), int(info[1])
		at := 64*block + 11*(part-1)
		if part < 1 || part > 6 || at >= len(s.gridInfo) {
			unknown(0)
			return
		}
		p := append([]byte{mk2.WReplyGridInfo}, bytes.Repeat([]byte{0xFF}, 11)...)
		copy(p[1:], s.gridInfo[at:min(at+11, len(s.gridInfo))])
		reply(p)

	case mk2.WCmdSendSoftwareVersionPart0, mk2.WCmdSendSoftwareVersionPart1: // §7.3.1
		v := d.p.Version
		switch {
		case unsupported:
			unknown(0)
		case r.Cmd == mk2.WCmdSendSoftwareVersionPart1:
			reply(word(mk2.WReplySoftwareVersionPart1, uint16(v>>16)))
		case long:
			// <LoPart0> <HiPart0> <LoPart1> <HiPart1> XX XX
			reply(append(binary.LittleEndian.AppendUint32([]byte{mk2.WReplySoftwareVersionPart0}, v), 0, 0))
		default:
			reply(word(mk2.WReplySoftwareVersionPart0, uint16(v)))
		}

	case mk2.WCmdGetSetDeviceState: // §7.3.2
		if unsupported {
			unknown(0)
			return
		}
		switch info[0] {
		case mk2.DeviceStateInquire:
		case mk2.DeviceStateForceEqualise:
			d.state = mk2.DeviceState{State: 9, SubState: 7}
		case mk2.DeviceStateForceAbsorption:
			d.state = mk2.DeviceState{State: 9, SubState: 6}
		case mk2.DeviceStateForceFloat:
			d.state = mk2.DeviceState{State: 9, SubState: 3}
		default:
			unknown(1) // "requested state is not supported"
			return
		}
		reply([]byte{mk2.WReplyDeviceState, d.state.State, d.state.SubState})

	case mk2.WCmdReadRAMVar: // §7.3.3
		// One reply code covers all IDs, so it follows the first ID;
		// later unsupported IDs read as 0.
		if _, ok := d.ram[uint16(info[0])]; !ok || unsupported {
			reply([]byte{mk2.WReplyRAMNotSupported, 0, 0})
			return
		}
		p := []byte{mk2.WReplyReadRAMOK}
		for _, rid := range info[:max(len(r.Info), 1)] {
			p = binary.LittleEndian.AppendUint16(p, d.ram[uint16(rid)])
		}
		reply(p)

	case mk2.WCmdReadSetting: // §7.3.4
		v, ok := d.values[id]
		if !ok || unsupported {
			reply([]byte{mk2.WReplySettingNotSupported, 0, 0})
			return
		}
		reply(word(mk2.WReplyReadSettingOK, v))

	case mk2.WCmdWriteRAMVar, mk2.WCmdWriteSetting: // §7.3.5, §7.3.6: no reply
		if unsupported {
			unknown(0)
			return
		}
		d.pending, d.pendingID = r.Cmd, id

	case mk2.WCmdWriteData: // §7.3.7
		// The spec defines no failure reply; a write without a valid
		// target gets no reply.
		target, tid := d.pending, d.pendingID
		d.pending = 0
		switch {
		case unsupported:
			unknown(0)
		case target == mk2.WCmdWriteSetting && d.hasSetting(tid) && len(s.passwords) > 0 && d.settings[tid].AccessLevel > s.access:
			// With Config.AccessPasswords set, as after CommandWriteViaID: a
			// device answers 0x34 with 0x9B too (reverse-engineered, #9).
			// Without, any setting is written, as the spec has it.
			reply([]byte{mk2.WReplyAccessLevelRequired, 0, 0})
		case target == mk2.WCmdWriteSetting && d.hasSetting(tid):
			d.values[tid] = id
			reply([]byte{mk2.WReplyWriteSettingOK, 0, 0})
		case target == mk2.WCmdWriteRAMVar && d.hasRAM(tid):
			d.ram[tid] = id
			reply([]byte{mk2.WReplyWriteRAMOK, 0, 0})
		}

	case mk2.WCmdGetSettingInfo: // §7.3.8
		st, ok := d.settings[id]
		switch {
		case (!ok || unsupported) && long:
			reply([]byte{mk2.WReplyReadSettingOK})
		case !ok || unsupported:
			reply(word(mk2.WReplySettingInfoScale, 0))
		case long:
			p := []byte{mk2.WReplySettingInfoScale}
			for _, v := range []uint16{uint16(st.Sc), uint16(st.Offset), st.Default, st.Min, st.Max} {
				p = binary.LittleEndian.AppendUint16(p, v)
			}
			reply(append(p, st.AccessLevel))
		default:
			// Five W frames, at most three per MK2 frame (§7.3).
			reply(
				slices.Concat(word(mk2.WReplySettingInfoScale, uint16(st.Sc)),
					word(mk2.WReplySettingInfoOffset, uint16(st.Offset)),
					word(mk2.WReplySettingInfoDefault, st.Default)),
				slices.Concat(word(mk2.WReplySettingInfoMin, st.Min),
					word(mk2.WReplySettingInfoMax, st.Max)))
		}

	case mk2.WCmdGetRAMVarInfo: // §7.3.9
		ri, ok := d.ramInfo[id]
		switch {
		case (!ok || unsupported) && long:
			reply([]byte{mk2.WReplyRAMInfoScale, 0, 0, 0, 0})
		case !ok || unsupported:
			reply(word(mk2.WReplyRAMInfoScale, 0))
		case long:
			reply(binary.LittleEndian.AppendUint16(word(mk2.WReplyRAMInfoScale, uint16(ri.Sc)), uint16(ri.Offset)))
		default:
			reply(slices.Concat(word(mk2.WReplyRAMInfoScale, uint16(ri.Sc)), word(mk2.WReplyRAMInfoOffset, uint16(ri.Offset))))
		}

	case mk2.WCmdWriteViaID: // §7.3.11: <flags> <ID> <Lo(data)> <Hi(data)>
		if !long || len(r.Info) < 4 || unsupported {
			unknown(0)
			return
		}
		flags, vid := r.Info[0], uint16(r.Info[1])
		v := binary.LittleEndian.Uint16(r.Info[2:])
		if flags&mk2.ViaIDSetting == 0 {
			if !d.hasRAM(vid) {
				unknown(2) // <Info1>, the ID, not recognised
				return
			}
			d.ram[vid] = v
			reply([]byte{mk2.WReplyWriteRAMOK, 0, 0})
			return
		}
		st, ok := d.settings[vid]
		switch {
		case !ok:
			unknown(2)
		case st.AccessLevel > s.access:
			reply([]byte{mk2.WReplyAccessLevelRequired, 0, 0})
		default:
			// ViaIDRAMOnly is accepted; the simulator has no separate
			// EEPROM copy.
			d.values[vid] = v
			reply([]byte{mk2.WReplyWriteSettingOK, 0, 0})
		}

	case mk2.WCmdReadSnapShot: // §7.3.12: read once
		if unsupported {
			unknown(0)
			return
		}
		p := []byte{mk2.WReplySnapshot}
		if d.hasSnapshot {
			for _, v := range d.snapshot {
				p = binary.LittleEndian.AppendUint16(p, v)
			}
			d.hasSnapshot = false
		}
		reply(p)

	case WCmdBlockRead, WCmdBlockWrite: // the simulator's own codec (assistants.go)
		if unsupported {
			unknown(0)
			return
		}
		if p := d.area.block(r.Cmd, r.Info); p != nil {
			reply(p)
			return
		}
		unknown(0)

	default:
		unknown(0)
	}
}

func (d *device) hasSetting(id uint16) bool { _, ok := d.settings[id]; return ok }
func (d *device) hasRAM(id uint16) bool     { _, ok := d.ramInfo[id]; return ok }

// word returns the W frame <code> <Lo(v)> <Hi(v)>.
func word(code byte, v uint16) []byte {
	return binary.LittleEndian.AppendUint16([]byte{code}, v)
}
