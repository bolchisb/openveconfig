package mk2

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// MK2 commands (§3-§7).
const (
	CmdVersion = 'V' // §3.4
	CmdLED     = 'L' // §4
	CmdState   = 'S' // §5
	CmdFrame   = 'F' // §6
	CmdAddress = 'A' // §7.1
	CmdWinmon  = 'W' // §7.3
	CmdReset   = 'R' // §3.6
)

// VE.Bus frame types sent in reply to 'F' or forwarded by the MK2 (§5.1, §6).
const (
	FrameInfo           = 0x20 // §6.1
	FrameBOL            = 0x3C // §6.4 reply
	FramePanel          = 0x40 // §5.1
	FrameMasterMultiLED = 0x41 // §6.2
)

// 'F' frame types (§6).
const (
	FTypeDC             = 0 // 1-4 are AC L1-L4
	FTypeMasterMultiLED = 5
	FTypeSnapshot       = 6
	FTypeResetDevice    = 8
	FTypeBOL            = 9
)

// Version 'V' <Mode> values (§3.4). Any other value is the address set in
// the MK2.
const (
	VersionModeRS485     = 'W' // target uses the VE 9-bit RS485 protocol
	VersionModeNoAddress = 'B' // VE.Bus, no address set
)

// AddressNone is the MK2's address after start-up; it is not valid (§7.1).
const AddressNone = 0xFF

// MaxAddress is the highest valid device address (§7.1).
const MaxAddress = 0x1F

// Version is the reply to 'V' (§3.4).
type Version struct {
	Version uint32
	Mode    byte
}

// ParseVersion decodes a 'V' frame (reply or unsolicited).
func ParseVersion(f Frame) (Version, error) {
	if f.VEBus || f.Command != CmdVersion {
		return Version{}, fmt.Errorf("mk2: not a version frame")
	}
	if len(f.Data) < 5 {
		return Version{}, ErrShortReply
	}
	return Version{Version: binary.LittleEndian.Uint32(f.Data), Mode: f.Data[4]}, nil
}

// Version requests the MK2 version frame (§3.4). The MK2 also sends this
// frame on its own about once per second; either one answers the request.
func (c *Client) Version(ctx context.Context) (Version, error) {
	var v Version
	err := c.do(func() error {
		return c.exchange(ctx, &Frame{Command: CmdVersion}, c.opts.Timeout, func(f Frame) (bool, bool, error) {
			p, err := ParseVersion(f)
			if err != nil {
				return false, false, nil
			}
			v = p
			return true, true, nil
		})
	})
	return v, err
}

// LED requests the LED status (§4).
func (c *Client) LED(ctx context.Context) (LEDStatus, error) {
	var s LEDStatus
	err := c.do(func() error {
		return c.exchange(ctx, &Frame{Command: CmdLED}, c.opts.Timeout, func(f Frame) (bool, bool, error) {
			if f.VEBus || f.Command != CmdLED || len(f.Data) < 2 {
				return false, false, nil
			}
			s = LEDStatus{On: f.Data[0], Blink: f.Data[1]}
			return true, true, nil
		})
	})
	return s, err
}

// Switch is the 'S' <Switch state> (§5).
type Switch byte

const (
	SwitchChargerOnly  Switch = 1
	SwitchInverterOnly Switch = 2
	SwitchOn           Switch = 3
	SwitchOff          Switch = 4
)

// 'S' <Flags> bits (§5).
const (
	StateFlagAutoSend      = 1 << 0 // send panel state every 0.5 s
	StateFlagAppendLED     = 1 << 1 // append LED status to all frames
	StateFlagNoSend        = 1 << 4 // do not send panel state
	StateFlagForwardPanel  = 1 << 6 // forward received panel frames
	StateFlagLimitDeciAmps = 1 << 7 // variant 2: limit in 0.1 A
)

// 'S' <FlagsExt0> bits (§5).
const (
	FlagsExt0WinmonShort      = 1 << 0 // 1 = mode 1 (default), 0 = mode 2
	FlagsExt0ForwardAllConfig = 1 << 2 // forward config responses from all devices
	FlagsExt0IgnoreBlockMode  = 1 << 3 // ignore block mode requests
)

// 'S' <EEPROMFlags> bits (§5).
const (
	EEPROMFlagForceVEBus = 1 << 0 // MK2 only: skip VE.Bus/VE9bit autodetect
	EEPROMFlagListenOnly = 1 << 1 // block transmit
)

// State is the 'S' command (§5).
type State struct {
	Switch Switch
	// Pot and Scale are used when Flags&StateFlagLimitDeciAmps == 0
	// (variant 1): set point = Pot * Scale / 256 A.
	Pot, Scale byte
	// Limit is used when Flags&StateFlagLimitDeciAmps != 0 (variant 2): the
	// input current limit in 0.1 A; >= 0x8000 means ignore.
	Limit uint16
	Flags byte
	// Ext, if non-nil, appends the optional <Reserved> <FlagsExt0>
	// <EEPROMFlags> bytes.
	Ext *StateExt
}

// StateExt holds the optional bytes of 'S' (§5).
type StateExt struct {
	FlagsExt0   byte
	EEPROMFlags byte
}

// Frame returns the 'S' request frame.
func (s State) Frame() Frame {
	lo, hi := s.Pot, s.Scale
	if s.Flags&StateFlagLimitDeciAmps != 0 {
		lo, hi = byte(s.Limit), byte(s.Limit>>8)
	}
	d := []byte{byte(s.Switch), lo, hi, 0x01, s.Flags}
	if s.Ext != nil {
		d = append(d, 0, s.Ext.FlagsExt0, s.Ext.EEPROMFlags)
	}
	return Frame{Command: CmdState, Data: d}
}

// SetState sends the panel state (§5). If s.Ext is set, the Client's Winmon
// mode follows FlagsExt0 bit 0.
func (c *Client) SetState(ctx context.Context, s State) error {
	req := s.Frame()
	return c.do(func() error {
		err := c.exchange(ctx, &req, c.opts.Timeout, func(f Frame) (bool, bool, error) {
			return !f.VEBus && f.Command == CmdState, true, nil
		})
		if err == nil && s.Ext != nil {
			c.mode = WinmonLong
			if s.Ext.FlagsExt0&FlagsExt0WinmonShort != 0 {
				c.mode = WinmonShort
			}
		}
		return err
	})
}

// address sends 'A' and returns the address the MK2 reports. c.mu is held.
func (c *Client) address(ctx context.Context, action, addr byte) (byte, error) {
	var got byte
	err := c.exchange(ctx, &Frame{Command: CmdAddress, Data: []byte{action, addr}}, c.opts.Timeout,
		func(f Frame) (bool, bool, error) {
			if f.VEBus || f.Command != CmdAddress || len(f.Data) < 2 || f.Data[0] != action {
				return false, false, nil
			}
			got = f.Data[1]
			return true, true, nil
		})
	return got, err
}

// SetAddress selects the device that 'W' commands go to (§7.1). Valid
// addresses are 0 to MaxAddress.
func (c *Client) SetAddress(ctx context.Context, addr byte) error {
	return c.do(func() error {
		got, err := c.address(ctx, 1, addr)
		if err == nil && got != addr {
			err = fmt.Errorf("mk2: address 0x%02X not accepted, MK2 uses 0x%02X", addr, got)
		}
		return err
	})
}

// Address returns the address currently set in the MK2 (§7.1); AddressNone
// if none is set.
func (c *Client) Address(ctx context.Context) (byte, error) {
	var got byte
	err := c.do(func() (err error) {
		got, err = c.address(ctx, 0, 0)
		return err
	})
	return got, err
}

// Discover finds the devices on the bus (§7.2): it selects each address from
// 0 and sends CommandSendSoftwareVersionPart0; any reply means a device is
// present. It stops after 3 consecutive addresses without a reply. The last
// probed address stays selected.
func (c *Client) Discover(ctx context.Context) ([]byte, error) {
	var found []byte
	misses := 0
	for a := byte(0); a <= MaxAddress && misses < 3; a++ {
		if err := c.SetAddress(ctx, a); err != nil {
			return found, err
		}
		_, err := c.Winmon(ctx, WCmdSendSoftwareVersionPart0)
		switch {
		case err == nil:
			found = append(found, a)
			misses = 0
		case errors.Is(err, ErrTimeout):
			misses++
		default:
			// Any reply, including "unknown command", means a device answered.
			var uc *UnknownCommandError
			if !errors.As(err, &uc) {
				return found, err
			}
			found = append(found, a)
			misses = 0
		}
	}
	return found, nil
}

// Reset resets the MK2 (§3.6). There is no reply.
func (c *Client) Reset(ctx context.Context) error {
	return c.do(func() error {
		return c.exchange(ctx, &Frame{Command: CmdReset}, 0, nil)
	})
}

// DCInfo is the DC info frame (§6.1), the reply to 'F' 0. Raw values use
// the scale and offset of the matching RAM variables (UBat, IBat, inverter
// period).
type DCInfo struct {
	Voltage          uint16
	CurrentInverting uint32 // unsigned 24-bit
	CurrentCharging  uint32 // unsigned 24-bit
	InverterPeriod   byte
}

// ACInfo is an AC info frame (§6.1), the reply to 'F' 1-4. Raw values use
// the scale and offset of the matching RAM variables.
type ACInfo struct {
	BFFactor        byte
	InverterFactor  byte
	State           byte // Multi firmware >= 207
	PhaseInfo       byte
	MainsVoltage    uint16
	MainsCurrent    uint16 // multiply by BFFactor for the phase total
	InverterVoltage uint16
	InverterCurrent uint16 // multiply by InverterFactor for the phase total
	MainsPeriod     byte
}

// <Phase info> values (§6.1).
const (
	PhaseL4      = 0x05
	PhaseL3      = 0x06
	PhaseL2      = 0x07
	PhaseL1Of1   = 0x08 // L1, 1 phase in the system; L1 of 2/3/4 phases is 0x09-0x0B
	PhaseL1Of4   = 0x0B
	PhaseDCFrame = 0x0C
)

// infoLen is the number of data bytes of an info frame after its type.
const infoLen = 14

// ParseDCInfo decodes a DC info frame (§6.1).
func ParseDCInfo(f Frame) (DCInfo, error) {
	d := f.Data
	if !f.VEBus || f.Command != FrameInfo || len(d) < infoLen || d[4] != PhaseDCFrame {
		return DCInfo{}, fmt.Errorf("mk2: not a DC info frame")
	}
	return DCInfo{
		Voltage:          binary.LittleEndian.Uint16(d[5:]),
		CurrentInverting: uint24(d[7:]),
		CurrentCharging:  uint24(d[10:]),
		InverterPeriod:   d[13],
	}, nil
}

// ParseACInfo decodes an AC info frame (§6.1).
func ParseACInfo(f Frame) (ACInfo, error) {
	d := f.Data
	if !f.VEBus || f.Command != FrameInfo || len(d) < infoLen || d[4] < PhaseL4 || d[4] > PhaseL1Of4 {
		return ACInfo{}, fmt.Errorf("mk2: not an AC info frame")
	}
	return ACInfo{
		BFFactor:        d[0],
		InverterFactor:  d[1],
		State:           d[3],
		PhaseInfo:       d[4],
		MainsVoltage:    binary.LittleEndian.Uint16(d[5:]),
		MainsCurrent:    binary.LittleEndian.Uint16(d[7:]),
		InverterVoltage: binary.LittleEndian.Uint16(d[9:]),
		InverterCurrent: binary.LittleEndian.Uint16(d[11:]),
		MainsPeriod:     d[13],
	}, nil
}

func uint24(b []byte) uint32 { return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 }

// requestFrame sends 'F' typ data and returns the first VE.Bus frame
// accepted by match.
func (c *Client) requestFrame(ctx context.Context, typ byte, timeout time.Duration, match func(Frame) bool) (Frame, error) {
	var got Frame
	err := c.do(func() error {
		return c.exchange(ctx, &Frame{Command: CmdFrame, Data: []byte{typ}}, timeout, func(f Frame) (bool, bool, error) {
			if !f.VEBus || !match(f) {
				return false, false, nil
			}
			got = f
			return true, true, nil
		})
	})
	return got, err
}

// DCInfo requests the DC info frame ('F' 0, §6.1).
func (c *Client) DCInfo(ctx context.Context) (DCInfo, error) {
	f, err := c.requestFrame(ctx, FTypeDC, c.opts.Timeout, func(f Frame) bool {
		_, err := ParseDCInfo(f)
		return err == nil
	})
	if err != nil {
		return DCInfo{}, err
	}
	return ParseDCInfo(f)
}

// ACInfo requests the AC info frame of phase 1-4 ('F' 1-4, §6.1).
func (c *Client) ACInfo(ctx context.Context, phase int) (ACInfo, error) {
	if phase < 1 || phase > 4 {
		return ACInfo{}, fmt.Errorf("mk2: invalid phase %d", phase)
	}
	f, err := c.requestFrame(ctx, byte(phase), c.opts.Timeout, func(f Frame) bool {
		info, err := ParseACInfo(f)
		if err != nil {
			return false
		}
		if phase == 1 {
			return info.PhaseInfo >= PhaseL1Of1
		}
		return info.PhaseInfo == PhaseL1Of1-byte(phase-1)
	})
	if err != nil {
		return ACInfo{}, err
	}
	return ParseACInfo(f)
}

// MasterMultiLED is the MasterMultiLED frame (§6.2), the reply to 'F' 5.
// Current limits are in 0.1 A.
type MasterMultiLED struct {
	Reserved         [4]byte // data bytes 0-3; §6.2 marks them reserved
	ACInputConfig    byte
	MinInputLimit    uint16
	MaxInputLimit    uint16
	ActualInputLimit uint16
	// SwitchRegister is data byte 11; older devices omit it
	// (HasSwitchRegister false).
	SwitchRegister    byte
	HasSwitchRegister bool
}

// ParseMasterMultiLED decodes a MasterMultiLED frame (§6.2).
func ParseMasterMultiLED(f Frame) (MasterMultiLED, error) {
	d := f.Data
	if !f.VEBus || f.Command != FrameMasterMultiLED || len(d) < 11 {
		return MasterMultiLED{}, fmt.Errorf("mk2: not a MasterMultiLED frame")
	}
	m := MasterMultiLED{
		Reserved:         [4]byte(d[:4]),
		ACInputConfig:    d[4],
		MinInputLimit:    binary.LittleEndian.Uint16(d[5:]),
		MaxInputLimit:    binary.LittleEndian.Uint16(d[7:]),
		ActualInputLimit: binary.LittleEndian.Uint16(d[9:]),
	}
	if len(d) >= 12 {
		m.SwitchRegister, m.HasSwitchRegister = d[11], true
	}
	return m, nil
}

// MasterMultiLED requests the MasterMultiLED frame ('F' 5, §6.2). It uses
// Options.SlowTimeout.
func (c *Client) MasterMultiLED(ctx context.Context) (MasterMultiLED, error) {
	f, err := c.requestFrame(ctx, FTypeMasterMultiLED, c.opts.SlowTimeout, func(f Frame) bool {
		return f.Command == FrameMasterMultiLED
	})
	if err != nil {
		return MasterMultiLED{}, err
	}
	return ParseMasterMultiLED(f)
}

// TakeSnapshot asks every device to snapshot up to 6 RAM variables ('F' 6,
// §6). There is no reply; read the values per device with ReadSnapshot.
func (c *Client) TakeSnapshot(ctx context.Context, ids ...byte) error {
	if len(ids) == 0 || len(ids) > 6 {
		return fmt.Errorf("mk2: snapshot needs 1 to 6 RAM IDs, got %d", len(ids))
	}
	req := Frame{Command: CmdFrame, Data: append([]byte{FTypeSnapshot}, ids...)}
	return c.do(func() error { return c.exchange(ctx, &req, 0, nil) })
}

// ResetDevice resets the VE.Bus device with the given long address, or all
// devices if it is 0 ('F' 8, §6.3). There is no reply.
func (c *Client) ResetDevice(ctx context.Context, longAddr uint32) error {
	req := Frame{Command: CmdFrame, Data: []byte{FTypeResetDevice,
		byte(longAddr >> 16), byte(longAddr >> 24), byte(longAddr), byte(longAddr >> 8)}}
	return c.do(func() error { return c.exchange(ctx, &req, 0, nil) })
}

// BOL <Flags> bits (§6.4).
const (
	BOLUBatCharge    = 1 << 0
	BOLIBatCharge    = 1 << 1
	BOLUBatDischarge = 1 << 2
	BOLIBatDischarge = 1 << 3
	BOLInfoMissing   = 1 << 7 // reply only: timeout
)

// BOL holds Battery Operational Limits (§6.4). Flags selects which fields
// are present. Voltages are in 0.01 V, currents in 0.1 A (24-bit).
type BOL struct {
	Flags         byte
	UBatCharge    uint16
	IBatCharge    uint32
	UBatDischarge uint16
	IBatDischarge uint32
}

// Encode returns <Flags> followed by the fields Flags selects.
func (b BOL) Encode() []byte {
	d := []byte{b.Flags}
	if b.Flags&BOLUBatCharge != 0 {
		d = binary.LittleEndian.AppendUint16(d, b.UBatCharge)
	}
	if b.Flags&BOLIBatCharge != 0 {
		d = append(d, byte(b.IBatCharge), byte(b.IBatCharge>>8), byte(b.IBatCharge>>16))
	}
	if b.Flags&BOLUBatDischarge != 0 {
		d = binary.LittleEndian.AppendUint16(d, b.UBatDischarge)
	}
	if b.Flags&BOLIBatDischarge != 0 {
		d = append(d, byte(b.IBatDischarge), byte(b.IBatDischarge>>8), byte(b.IBatDischarge>>16))
	}
	return d
}

// ParseBOL decodes the fields of a BOL frame, d being <Flags> onwards.
func ParseBOL(d []byte) (BOL, error) {
	if len(d) < 1 {
		return BOL{}, ErrShortReply
	}
	b := BOL{Flags: d[0]}
	d = d[1:]
	take := func(n int) ([]byte, error) {
		if len(d) < n {
			return nil, ErrShortReply
		}
		v := d[:n]
		d = d[n:]
		return v, nil
	}
	for _, fld := range []struct {
		bit byte
		n   int
		set func([]byte)
	}{
		{BOLUBatCharge, 2, func(v []byte) { b.UBatCharge = binary.LittleEndian.Uint16(v) }},
		{BOLIBatCharge, 3, func(v []byte) { b.IBatCharge = uint24(v) }},
		{BOLUBatDischarge, 2, func(v []byte) { b.UBatDischarge = binary.LittleEndian.Uint16(v) }},
		{BOLIBatDischarge, 3, func(v []byte) { b.IBatDischarge = uint24(v) }},
	} {
		if b.Flags&fld.bit == 0 {
			continue
		}
		v, err := take(fld.n)
		if err != nil {
			return BOL{}, err
		}
		fld.set(v)
	}
	return b, nil
}

// SendBOL sends Battery Operational Limits ('F' 9, §6.4) and returns the
// device's BOL reply. Requires Multi firmware >= 415 and MK3 firmware >= 203;
// the MK2 does not support it.
func (c *Client) SendBOL(ctx context.Context, b BOL) (BOL, error) {
	req := Frame{Command: CmdFrame, Data: append([]byte{FTypeBOL}, b.Encode()...)}
	var reply BOL
	err := c.do(func() error {
		return c.exchange(ctx, &req, c.opts.Timeout, func(f Frame) (bool, bool, error) {
			if !f.VEBus || f.Command != FrameBOL {
				return false, false, nil
			}
			r, err := ParseBOL(f.Data)
			reply = r
			return true, true, err
		})
	})
	return reply, err
}
