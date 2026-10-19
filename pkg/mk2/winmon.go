package mk2

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

// Winmon commands (§7.3).
const (
	WCmdSendSoftwareVersionPart0 = 0x05 // §7.3.1
	WCmdSendSoftwareVersionPart1 = 0x06 // §7.3.1, mode 1 only
	WCmdGetSetDeviceState        = 0x0E // §7.3.2
	WCmdReadRAMVar               = 0x30 // §7.3.3
	WCmdReadSetting              = 0x31 // §7.3.4
	WCmdWriteRAMVar              = 0x32 // §7.3.5, no reply
	WCmdWriteSetting             = 0x33 // §7.3.6, no reply
	WCmdWriteData                = 0x34 // §7.3.7
	WCmdGetSettingInfo           = 0x35 // §7.3.8
	WCmdGetRAMVarInfo            = 0x36 // §7.3.9
	WCmdWriteViaID               = 0x37 // §7.3.11, mode 2 only
	WCmdReadSnapShot             = 0x38 // §7.3.12
)

// Winmon replies (§7.3).
const (
	WReplyUnknownCommand       = 0x80
	WReplySoftwareVersionPart0 = 0x82
	WReplySoftwareVersionPart1 = 0x83
	WReplyReadRAMOK            = 0x85
	WReplyReadSettingOK        = 0x86 // also "setting not supported" for GetSettingInfo in mode 2
	WReplyWriteRAMOK           = 0x87
	WReplyWriteSettingOK       = 0x88
	WReplySettingInfoScale     = 0x89 // mode 2: carries all setting info
	WReplySettingInfoOffset    = 0x8A
	WReplySettingInfoDefault   = 0x8B
	WReplySettingInfoMin       = 0x8C
	WReplySettingInfoMax       = 0x8D
	WReplyRAMInfoScale         = 0x8E // mode 2: carries scale and offset
	WReplyRAMInfoOffset        = 0x8F
	WReplyRAMNotSupported      = 0x90
	WReplySettingNotSupported  = 0x91
	WReplyDeviceState          = 0x94
	WReplySnapshot             = 0x99
	WReplyAccessLevelRequired  = 0x9B
)

// CommandWriteViaID <flags> bits (§7.3.11).
const (
	ViaIDSetting = 1 << 0 // 0 = RAM variable, 1 = setting
	ViaIDRAMOnly = 1 << 1 // 0 = update RAM and EEPROM, 1 = RAM only (firmware >= 412)
)

// CommandGetSetDeviceState <State> requests (§7.3.2).
const (
	DeviceStateInquire         = 0
	DeviceStateForceEqualise   = 1
	DeviceStateForceAbsorption = 2
	DeviceStateForceFloat      = 3
)

// DeviceState is the reply to CommandGetSetDeviceState (§7.3.2).
type DeviceState struct {
	State    byte // 0 down ... 9 charge
	SubState byte // charge sub-state when State is 9
}

// WinmonRequest is one Winmon command inside its MK2 frame (§7.3):
// <Tag> <W command> <Info...> [<Dest>].
type WinmonRequest struct {
	// Tag is 'W', 'X', 'Y', 'Z', or 'x', 'y', 'z' for the addressed form.
	Tag  byte
	Cmd  byte
	Info []byte
	// Dest is the destination address, sent only with 'x', 'y', 'z'.
	Dest byte
}

// Frame returns the MK2 frame for r.
func (r WinmonRequest) Frame() Frame {
	d := append([]byte{r.Cmd}, r.Info...)
	if isAddressedTag(r.Tag) {
		d = append(d, r.Dest)
	}
	return Frame{Command: r.Tag, Data: d}
}

// ParseWinmonRequest decodes a Winmon request frame, as a device would.
func ParseWinmonRequest(f Frame) (WinmonRequest, error) {
	if f.VEBus || ReplyTag(f.Command) == 0 {
		return WinmonRequest{}, fmt.Errorf("mk2: not a Winmon request")
	}
	d := f.Data
	r := WinmonRequest{Tag: f.Command}
	if isAddressedTag(f.Command) {
		if len(d) < 2 {
			return WinmonRequest{}, ErrShortFrame
		}
		r.Dest = d[len(d)-1]
		d = d[:len(d)-1]
	}
	if len(d) < 1 {
		return WinmonRequest{}, ErrShortFrame
	}
	r.Cmd = d[0]
	r.Info = d[1:]
	return r, nil
}

// ReplyTag returns the command letter of the reply to a Winmon request
// tag; replies are always uppercase (§7.3). It returns 0 for a byte that is
// not a Winmon tag.
func ReplyTag(tag byte) byte {
	switch tag {
	case 'W', 'X', 'Y', 'Z':
		return tag
	case 'x', 'y', 'z':
		return tag - 'a' + 'A'
	}
	return 0
}

func isAddressedTag(t byte) bool { return t == 'x' || t == 'y' || t == 'z' }

// WFrame is a 3-byte <W command> <Info0> <Info1> frame. Mode 1 replies
// pack up to three of them into one MK2 frame (§7.3).
type WFrame [3]byte

// SplitWFrames splits a mode 1 reply payload into W frames. Trailing bytes
// that do not form a whole frame are ignored.
func SplitWFrames(p []byte) []WFrame {
	var fs []WFrame
	for ; len(p) >= 3; p = p[3:] {
		fs = append(fs, WFrame(p[:3]))
	}
	return fs
}

func (w WFrame) value() uint16 { return binary.LittleEndian.Uint16(w[1:]) }

// collector receives reply payloads (the bytes after the tag letter, from
// the reply code on). See handler for the results.
type collector func(p []byte) (mine, done bool, err error)

// winmon sends a Winmon command. A nil collect means no reply is expected.
// c.mu must be held.
func (c *Client) winmon(ctx context.Context, cmd byte, info []byte, collect collector) error {
	// A W frame always has two info bytes (§7.3); pad shorter requests.
	if len(info) < 2 {
		info = append(append(make([]byte, 0, 2), info...), make([]byte, 2-len(info))...)
	}
	tag := byte('W')
	if c.opts.Tunnel != TunnelW {
		tag = "XYZ"[c.tag]
		if c.opts.Tunnel == TunnelXYZAddressed {
			tag = "xyz"[c.tag]
		}
		c.tag = (c.tag + 1) % 3
	}
	req := WinmonRequest{Tag: tag, Cmd: cmd, Info: info, Dest: c.dest}.Frame()
	if collect == nil {
		return c.exchange(ctx, &req, 0, nil)
	}
	reply := ReplyTag(tag)
	return c.exchange(ctx, &req, c.opts.Timeout, func(f Frame) (bool, bool, error) {
		if f.VEBus || f.Command != reply || len(f.Data) == 0 {
			return false, false, nil
		}
		if f.Data[0] == WReplyUnknownCommand {
			e := &UnknownCommandError{}
			if len(f.Data) > 1 {
				e.Reason = f.Data[1]
			}
			return true, true, e
		}
		return collect(f.Data)
	})
}

func le16(v uint16) []byte { return binary.LittleEndian.AppendUint16(nil, v) }

// Winmon sends any Winmon command and returns the payload of the first
// reply, from the reply code on. Use it for commands this package does not
// wrap. An "Unknown command" reply returns *UnknownCommandError.
func (c *Client) Winmon(ctx context.Context, cmd byte, info ...byte) ([]byte, error) {
	var p []byte
	err := c.do(func() error {
		return c.winmon(ctx, cmd, info, func(r []byte) (bool, bool, error) {
			p = r
			return true, true, nil
		})
	})
	return p, err
}

// SoftwareVersion returns the target device's firmware version (§7.3.1).
func (c *Client) SoftwareVersion(ctx context.Context) (uint32, error) {
	var v uint32
	err := c.do(func() error {
		if c.mode == WinmonLong {
			return c.winmon(ctx, WCmdSendSoftwareVersionPart0, nil, func(p []byte) (bool, bool, error) {
				if p[0] != WReplySoftwareVersionPart0 {
					return false, false, nil
				}
				if len(p) < 5 {
					return true, true, ErrShortReply
				}
				v = binary.LittleEndian.Uint32(p[1:])
				return true, true, nil
			})
		}
		var parts [2]uint16
		for i, cmd := range []byte{WCmdSendSoftwareVersionPart0, WCmdSendSoftwareVersionPart1} {
			err := c.winmon(ctx, cmd, nil, func(p []byte) (bool, bool, error) {
				if p[0] != WReplySoftwareVersionPart0+byte(i) {
					return false, false, nil
				}
				if len(p) < 3 {
					return true, true, ErrShortReply
				}
				parts[i] = binary.LittleEndian.Uint16(p[1:])
				return true, true, nil
			})
			if err != nil {
				return err
			}
		}
		v = uint32(parts[0]) | uint32(parts[1])<<16
		return nil
	})
	return v, err
}

// DeviceState reads the device state, or forces a charge state when req is
// not DeviceStateInquire (§7.3.2).
func (c *Client) DeviceState(ctx context.Context, req byte) (DeviceState, error) {
	var s DeviceState
	err := c.do(func() error {
		return c.winmon(ctx, WCmdGetSetDeviceState, []byte{req}, func(p []byte) (bool, bool, error) {
			if p[0] != WReplyDeviceState {
				return false, false, nil
			}
			if len(p) < 3 {
				return true, true, ErrShortReply
			}
			s = DeviceState{State: p[1], SubState: p[2]}
			return true, true, nil
		})
	})
	return s, err
}

// ReadRAMVar reads 1 to 6 RAM variables (§7.3.3). With a single ID the
// request is padded with ID 0, as in the spec's example; newer devices then
// return its value too, which is ignored.
func (c *Client) ReadRAMVar(ctx context.Context, ids ...byte) ([]uint16, error) {
	if len(ids) == 0 || len(ids) > 6 {
		return nil, fmt.Errorf("mk2: ReadRAMVar needs 1 to 6 IDs, got %d", len(ids))
	}
	var vals []uint16
	err := c.do(func() error {
		return c.winmon(ctx, WCmdReadRAMVar, ids, func(p []byte) (bool, bool, error) {
			switch p[0] {
			case WReplyRAMNotSupported:
				return true, true, ErrNotSupported
			case WReplyReadRAMOK:
				if len(p) < 1+2*len(ids) {
					return true, true, ErrShortReply
				}
				vals = make([]uint16, len(ids))
				for i := range vals {
					vals[i] = binary.LittleEndian.Uint16(p[1+2*i:])
				}
				return true, true, nil
			}
			return false, false, nil
		})
	})
	return vals, err
}

// ReadSetting reads a setting (§7.3.4).
func (c *Client) ReadSetting(ctx context.Context, id uint16) (uint16, error) {
	var v uint16
	err := c.do(func() error {
		return c.winmon(ctx, WCmdReadSetting, le16(id), func(p []byte) (bool, bool, error) {
			switch p[0] {
			case WReplySettingNotSupported:
				return true, true, ErrNotSupported
			case WReplyReadSettingOK:
				if len(p) < 3 {
					return true, true, ErrShortReply
				}
				v = binary.LittleEndian.Uint16(p[1:])
				return true, true, nil
			}
			return false, false, nil
		})
	})
	return v, err
}

// WriteSetting writes a setting with the method chosen by
// Options.WriteMethod: 0x33 then 0x34 (§7.3.6-7), or 0x37 (§7.3.11).
func (c *Client) WriteSetting(ctx context.Context, id uint16, value uint16) error {
	if c.opts.WriteMethod == WriteMethodViaID {
		if id > 0xFF {
			return fmt.Errorf("mk2: WriteViaID takes an 8-bit ID, got %d", id)
		}
		return c.WriteViaID(ctx, ViaIDSetting, byte(id), value)
	}
	return c.writeData(ctx, WCmdWriteSetting, id, value, WReplyWriteSettingOK)
}

// WriteRAMVar writes a RAM variable with the method chosen by
// Options.WriteMethod: 0x32 then 0x34 (§7.3.5, §7.3.7), or 0x37 (§7.3.11).
func (c *Client) WriteRAMVar(ctx context.Context, id uint16, value uint16) error {
	if c.opts.WriteMethod == WriteMethodViaID {
		if id > 0xFF {
			return fmt.Errorf("mk2: WriteViaID takes an 8-bit ID, got %d", id)
		}
		return c.WriteViaID(ctx, 0, byte(id), value)
	}
	return c.writeData(ctx, WCmdWriteRAMVar, id, value, WReplyWriteRAMOK)
}

func (c *Client) writeData(ctx context.Context, cmd byte, id, value uint16, ok byte) error {
	err := c.do(func() error {
		if err := c.winmon(ctx, cmd, le16(id), nil); err != nil {
			return err
		}
		return c.winmon(ctx, WCmdWriteData, le16(value), func(p []byte) (bool, bool, error) {
			return p[0] == ok, p[0] == ok, nil
		})
	})
	return c.ackErr(err)
}

// WriteViaID writes a RAM variable or setting with CommandWriteViaID
// (§7.3.11). flags is a combination of ViaIDSetting and ViaIDRAMOnly. The
// spec marks the command "Winmon mode 2 only".
func (c *Client) WriteViaID(ctx context.Context, flags, id byte, value uint16) error {
	ok := byte(WReplyWriteRAMOK)
	if flags&ViaIDSetting != 0 {
		ok = WReplyWriteSettingOK
	}
	info := append([]byte{flags, id}, le16(value)...)
	err := c.do(func() error {
		return c.winmon(ctx, WCmdWriteViaID, info, func(p []byte) (bool, bool, error) {
			switch p[0] {
			case ok:
				return true, true, nil
			case WReplyAccessLevelRequired:
				return true, true, ErrAccessLevel
			}
			return false, false, nil
		})
	})
	return c.ackErr(err)
}

func (c *Client) ackErr(err error) error {
	if c.opts.IgnoreMissingWriteAck && errors.Is(err, ErrTimeout) {
		return nil
	}
	return err
}

// SettingInfo reads the scale, offset, default, minimum and maximum of a
// setting (§7.3.8). It returns ErrNotSupported for unsupported settings.
// The command code is Options.SettingInfoCommand.
func (c *Client) SettingInfo(ctx context.Context, id uint16) (SettingInfo, error) {
	var s SettingInfo
	var seen byte // mode 1: bit n set when reply 0x89+n arrived
	long := func(p []byte) (bool, bool, error) {
		switch p[0] {
		case WReplyReadSettingOK:
			return true, true, ErrNotSupported
		case WReplySettingInfoScale:
		default:
			return false, false, nil
		}
		if len(p) < 11 {
			return true, true, ErrShortReply
		}
		s = SettingInfo{
			Sc:      int16(binary.LittleEndian.Uint16(p[1:])),
			Offset:  int16(binary.LittleEndian.Uint16(p[3:])),
			Default: binary.LittleEndian.Uint16(p[5:]),
			Min:     binary.LittleEndian.Uint16(p[7:]),
			Max:     binary.LittleEndian.Uint16(p[9:]),
		}
		if len(p) >= 12 {
			s.AccessLevel, s.HasAccessLevel = p[11], true
		}
		if s.Sc == 0 {
			return true, true, ErrNotSupported
		}
		return true, true, nil
	}
	// Mode 1 sends five W frames; at most three fit in one MK2 frame, so
	// the reply spans two frames (§7.3).
	short := func(p []byte) (bool, bool, error) {
		fs := SplitWFrames(p)
		if len(fs) == 0 || fs[0][0] < WReplySettingInfoScale || fs[0][0] > WReplySettingInfoMax {
			return false, false, nil
		}
		for _, w := range fs {
			switch w[0] {
			case WReplySettingInfoScale:
				s.Sc = int16(w.value())
				if s.Sc == 0 {
					return true, true, ErrNotSupported
				}
			case WReplySettingInfoOffset:
				s.Offset = int16(w.value())
			case WReplySettingInfoDefault:
				s.Default = w.value()
			case WReplySettingInfoMin:
				s.Min = w.value()
			case WReplySettingInfoMax:
				s.Max = w.value()
			default:
				continue
			}
			seen |= 1 << (w[0] - WReplySettingInfoScale)
		}
		return true, seen == 0x1F, nil
	}
	err := c.do(func() error {
		s, seen = SettingInfo{}, 0
		collect := short
		if c.mode == WinmonLong {
			collect = long
		}
		return c.winmon(ctx, c.opts.SettingInfoCommand, le16(id), collect)
	})
	return s, err
}

// RAMVarInfo reads the scale and offset of a RAM variable (§7.3.9). It
// returns ErrNotSupported for unsupported variables (Sc = 0).
func (c *Client) RAMVarInfo(ctx context.Context, id uint16) (RAMInfo, error) {
	var r RAMInfo
	var seen byte // mode 1: bit 0 scale, bit 1 offset
	long := func(p []byte) (bool, bool, error) {
		if p[0] != WReplyRAMInfoScale {
			return false, false, nil
		}
		if len(p) < 5 {
			return true, true, ErrShortReply
		}
		r = RAMInfo{
			Sc:     int16(binary.LittleEndian.Uint16(p[1:])),
			Offset: int16(binary.LittleEndian.Uint16(p[3:])),
		}
		if r.Sc == 0 {
			return true, true, ErrNotSupported
		}
		return true, true, nil
	}
	short := func(p []byte) (bool, bool, error) {
		fs := SplitWFrames(p)
		if len(fs) == 0 || (fs[0][0] != WReplyRAMInfoScale && fs[0][0] != WReplyRAMInfoOffset) {
			return false, false, nil
		}
		for _, w := range fs {
			switch w[0] {
			case WReplyRAMInfoScale:
				r.Sc = int16(w.value())
				if r.Sc == 0 {
					return true, true, ErrNotSupported
				}
				seen |= 1
			case WReplyRAMInfoOffset:
				r.Offset = int16(w.value())
				seen |= 2
			}
		}
		return true, seen == 3, nil
	}
	err := c.do(func() error {
		r, seen = RAMInfo{}, 0
		collect := short
		if c.mode == WinmonLong {
			collect = long
		}
		return c.winmon(ctx, WCmdGetRAMVarInfo, le16(id), collect)
	})
	return r, err
}

// ReadSnapshot reads the values of the last snapshot taken with
// TakeSnapshot (§7.3.12). A snapshot can be read only once.
func (c *Client) ReadSnapshot(ctx context.Context) ([]uint16, error) {
	var vals []uint16
	err := c.do(func() error {
		return c.winmon(ctx, WCmdReadSnapShot, nil, func(p []byte) (bool, bool, error) {
			if p[0] != WReplySnapshot {
				return false, false, nil
			}
			vals = nil
			for q := p[1:]; len(q) >= 2 && len(vals) < 6; q = q[2:] {
				vals = append(vals, binary.LittleEndian.Uint16(q))
			}
			return true, true, nil
		})
	})
	return vals, err
}
