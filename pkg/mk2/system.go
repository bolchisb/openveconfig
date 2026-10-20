package mk2

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// VE.Bus system frames.
//
// Reverse-engineered (#53), not in the MK2 protocol document: a tool reads
// and writes the system configuration of VE.Bus units (which phase and AC
// input each unit is on, the short addresses in the system) with raw
// frames that the MK passes to the bus. A raw frame is <n> <payload> <cs>,
// without the 0xFF marker, and its checksum follows its own rule
// (RawChecksum). The payload of a system frame is CmdSystem, the target's
// long ID and a sub-command. Units answer with VE.Bus frames of type
// FrameSystem, which carry the normal checksum, so Decode and ScanFrames
// read them. Whether the MK forwards raw frames unchanged has not been
// checked on hardware (#3).

// CmdSystem is the first payload byte of a raw system frame ('!').
// Reverse-engineered (#53).
const CmdSystem = 0x21

// FrameSystem is the VE.Bus frame type of the replies to system frames. It
// is the same as FrameInfo; the byte after the long ID tells them apart
// (see ParseIdentify and ParseSystemConfig). Reverse-engineered (#53).
const FrameSystem = FrameInfo

// LongIDAll addresses a system frame to every unit (broadcast).
// Reverse-engineered (#53).
const LongIDAll uint32 = 0

// System sub-commands, the payload byte after the long ID.
// Reverse-engineered (#53); the meanings marked INFERRED are our reading of
// the original tools' behaviour.
const (
	SysIdentify     = 0x00 // broadcast; one unit with identification on answers
	SysIdentifyMode = 0x01 // + 01 on / 00 off, no reply; a unit with identification off does not answer SysIdentify (INFERRED)
	SysFlashLEDs    = 0x02 // + 01 0F FF on / 00 off, no reply
	SysReset        = 0x03 // broadcast, no reply; restarts all units so a written record takes effect (INFERRED)
	SysWriteConfig  = 0x04 // + 9-byte record, no reply
	SysReadConfig   = 0x05 // reply: system configuration
	SysFreeze       = 0x06 // no reply; effect unknown
	SysKeepAlive    = 0x90 // broadcast + 03 14, no reply; meaning unknown
)

// Identify reply flags (reply byte 5). Reverse-engineered (#53).
const (
	IdentifyFlag = 0x01
	// IdentifyFlagAlt is accepted by the original tools only in a build
	// that is switched off; its meaning is unknown. Identify rejects it
	// unless Options.AcceptIdentify11 is set.
	IdentifyFlagAlt = 0x11
)

// LetterUnknown is Identity.Letter when the identify reply has no letter.
const LetterUnknown = 0xFF

// System configuration response characters (read reply byte 5) with a
// meaning of their own. Every other accepted value (0x02, 0x20-0x2E) is a
// single-input Multi. Reverse-engineered (#53).
const (
	ResponseCharDualInput = 0x03 // unit with two AC inputs
	ResponseCharInverter  = 0x04 // inverter, no AC input charging
	ResponseCharNoConfig  = 0x23 // '#': the unit has no system configuration
	ResponseCharMultiII   = 0x2F // '/': single-input Multi of the MultiPlus-II / MultiGrid family
)

// SystemRecordLen is the size of a unit's system configuration record.
const SystemRecordLen = 9

// Timeouts and periods of system frames.
const (
	// DefaultRawTimeout is the default Options.RawTimeout, the reply wait
	// the original tools use for system frames. Reverse-engineered (#53).
	DefaultRawTimeout = 5 * time.Second
	// DefaultKeepAlivePeriod is the period of StartKeepAlive our front ends
	// use. The original tools send the keep-alive every 3-5 s
	// (reverse-engineered, #53); 3 s is our choice.
	DefaultKeepAlivePeriod = 3 * time.Second
)

// systemTiming holds the delays of system requests. Tests shorten them.
var systemTiming = struct {
	// listen is how long Identify waits for an identify reply that is
	// already on its way before it sends SysIdentify. Reverse-engineered
	// (#53): the original tools do the same.
	listen time.Duration
	// spacing is the minimum gap after a system frame that expects no
	// reply. Our choice: the original tools hold the next frame for about
	// 60-94 ms (reverse-engineered, #53).
	spacing time.Duration
}{
	listen:  500 * time.Millisecond,
	spacing: 100 * time.Millisecond,
}

// System errors.
var (
	// ErrLinkMode is returned by system requests while the link is not
	// known to be in VE.Bus mode: PrepareSystemLink has not succeeded since
	// the Client started or was Reset, or the MK reported VE 9-bit RS485
	// mode. Nothing is sent.
	ErrLinkMode = errors.New("mk2: interface is not in VE.Bus mode")
	// ErrUnexpectedReply means a frame that looked like the reply arrived
	// but could not be accepted, such as a configuration reply with a
	// response character outside the accepted range.
	ErrUnexpectedReply = errors.New("mk2: unexpected reply")
)

var errRawMarker = errors.New("mk2: raw frame payload starts with 0xFF")

// RawChecksum returns the checksum of a raw frame with this payload.
// Reverse-engineered (#53): with n = len(payload), the checksum makes
// n + Σpayload ≡ 0 (mod 256), the rule of MK2 frames (§3.2), when n <= 4.
// When n >= 5, the nibble-swapped payload bytes 0, 2 and 4 are added to the
// sum as well.
func RawChecksum(payload []byte) byte {
	sum := byte(len(payload))
	for _, c := range payload {
		sum += c
	}
	if len(payload) >= 5 {
		sum += swapNibbles(payload[0]) + swapNibbles(payload[2]) + swapNibbles(payload[4])
	}
	return -sum
}

func swapNibbles(b byte) byte { return b<<4 | b>>4 }

// EncodeRaw returns the raw frame <n> <payload> <cs>. The payload must hold
// 1 to MaxBody bytes and must not start with 0xFF, which would make the
// frame read as an MK2 frame.
func EncodeRaw(payload []byte) ([]byte, error) {
	switch {
	case len(payload) == 0:
		return nil, ErrShortFrame
	case len(payload) > MaxBody:
		return nil, ErrTooLong
	case payload[0] == Marker:
		return nil, errRawMarker
	}
	b := make([]byte, 0, len(payload)+2)
	b = append(b, byte(len(payload)))
	b = append(b, payload...)
	return append(b, RawChecksum(payload)), nil
}

// DecodeRaw checks one raw frame, b, and returns a copy of its payload. It
// is the reverse of EncodeRaw, for the device side and frame logs.
func DecodeRaw(b []byte) ([]byte, error) {
	if len(b) < 3 || b[0] == 0 {
		return nil, ErrShortFrame
	}
	n := int(b[0])
	switch {
	case n > MaxBody:
		return nil, ErrTooLong
	case len(b) != n+2:
		return nil, ErrFrameSize
	case b[1] == Marker:
		return nil, errRawMarker
	case RawChecksum(b[1:n+1]) != b[n+1]:
		return nil, ErrChecksum
	}
	return bytes.Clone(b[1 : n+1]), nil
}

// SystemPayload returns the payload of a system frame: CmdSystem, longID
// (little-endian), sub and args.
func SystemPayload(longID uint32, sub byte, args ...byte) []byte {
	p := []byte{CmdSystem, 0, 0, 0, 0, sub}
	binary.LittleEndian.PutUint32(p[1:], longID)
	return append(p, args...)
}

// ParseSystemPayload decodes a system frame payload, as a unit would.
func ParseSystemPayload(p []byte) (longID uint32, sub byte, args []byte, err error) {
	if len(p) < 6 || p[0] != CmdSystem {
		return 0, 0, nil, fmt.Errorf("mk2: not a system frame")
	}
	return binary.LittleEndian.Uint32(p[1:]), p[5], p[6:], nil
}

// Identity is the identify reply, the answer to SysIdentify.
// Reverse-engineered (#53).
type Identity struct {
	LongID uint32
	// Flag is reply byte 5: IdentifyFlag, or IdentifyFlagAlt.
	Flag         byte
	ShortAddress byte // the unit's current VE.Bus address
	// VersionWord: the low 24 bits are the 7-digit firmware number, the
	// high 8 bits a sub-version.
	VersionWord uint32
	// Letter is the compatibility letter index, 0 = 'A' ... 25 = 'Z'
	// (larger values are unknown letters); LetterUnknown when absent.
	Letter byte
	// ConfigProtocol is the configuration-protocol version; 0 when
	// absent.
	ConfigProtocol byte
}

// Frame returns the identify reply for id, as a unit sends it.
func (id Identity) Frame() Frame {
	d := binary.LittleEndian.AppendUint32(nil, id.LongID)
	d = append(d, id.Flag, id.ShortAddress)
	d = binary.LittleEndian.AppendUint32(d, id.VersionWord)
	d = append(d, id.Letter, id.ConfigProtocol)
	return Frame{VEBus: true, Command: FrameSystem, Data: d}
}

// ParseIdentify decodes an identify reply. Reverse-engineered (#53): the
// letter and configuration-protocol bytes are optional, and a firmware
// number ending in 553 always reports configuration protocol 0. It accepts
// both IdentifyFlag and IdentifyFlagAlt.
func ParseIdentify(f Frame) (Identity, error) {
	d := f.Data
	if !f.VEBus || f.Command != FrameSystem || len(d) < 5 || d[4] != IdentifyFlag && d[4] != IdentifyFlagAlt {
		return Identity{}, fmt.Errorf("%w: not an identify reply", ErrUnexpectedReply)
	}
	if len(d) < 10 {
		return Identity{}, ErrShortReply
	}
	id := Identity{
		LongID:       binary.LittleEndian.Uint32(d),
		Flag:         d[4],
		ShortAddress: d[5],
		VersionWord:  binary.LittleEndian.Uint32(d[6:]),
		Letter:       LetterUnknown,
	}
	if len(d) >= 11 {
		id.Letter = d[10]
	}
	if len(d) >= 12 {
		id.ConfigProtocol = d[11]
	}
	if (id.VersionWord&0xFFFFFF)%1000 == 553 {
		id.ConfigProtocol = 0
	}
	return id, nil
}

// SystemConfig is the reply to SysReadConfig. Reverse-engineered (#53).
type SystemConfig struct {
	LongID uint32
	// ResponseChar is reply byte 5, 0x02-0x04 or 0x20-0x2F; see the
	// ResponseChar constants.
	ResponseChar byte
	Record       [SystemRecordLen]byte
}

// Frame returns the reply for s, as a unit sends it. Reply byte 6, whose
// meaning is unknown, is 0.
func (s SystemConfig) Frame() Frame {
	d := binary.LittleEndian.AppendUint32(nil, s.LongID)
	d = append(d, s.ResponseChar, 0)
	d = append(d, s.Record[:]...)
	return Frame{VEBus: true, Command: FrameSystem, Data: d}
}

// ParseSystemConfig decodes the reply of the unit longID to SysReadConfig.
// A reply from another unit, or an info frame (§6.1, whose byte 5 is
// 0x05-0x0C), is not accepted.
func ParseSystemConfig(f Frame, longID uint32) (SystemConfig, error) {
	d := f.Data
	if !f.VEBus || f.Command != FrameSystem || len(d) < 4 || binary.LittleEndian.Uint32(d) != longID {
		return SystemConfig{}, fmt.Errorf("%w: not a configuration reply of unit 0x%08X", ErrUnexpectedReply, longID)
	}
	if len(d) < 5 {
		return SystemConfig{}, ErrShortReply
	}
	if c := d[4]; c < 0x02 || c > 0x04 && c < 0x20 || c > 0x2F {
		return SystemConfig{}, fmt.Errorf("%w: response character 0x%02X", ErrUnexpectedReply, c)
	}
	if len(d) < 6+SystemRecordLen {
		return SystemConfig{}, ErrShortReply
	}
	return SystemConfig{LongID: longID, ResponseChar: d[4], Record: [SystemRecordLen]byte(d[6:])}, nil
}

// systemLinkState is the 'S' command of PrepareSystemLink, as the original
// tools send it (reverse-engineered, #53): switch value 4, pot 0, scale
// 0xFF, Flags = StateFlagNoSend so that the switch value is not applied,
// FlagsExt0 = FlagsExt0ForwardAllConfig (and Winmon mode 2), no
// EEPROMFlags.
var systemLinkState = State{
	Switch: SwitchOff,
	Scale:  0xFF,
	Flags:  StateFlagNoSend,
	Ext:    &StateExt{FlagsExt0: FlagsExt0ForwardAllConfig, OmitEEPROMFlags: true},
}

// PrepareSystemLink makes the link ready for system frames: it sends 'V'
// and fails with ErrLinkMode if the MK reports VE 9-bit RS485 mode (§3.4),
// then sets address 0 ('A', §7.1) and sends the 'S' command of the original
// tools (§5): StateFlagNoSend, FlagsExt0ForwardAllConfig and Winmon mode 2,
// without EEPROMFlags. So the Client's Winmon mode becomes WinmonLong.
//
// Call it at the start of every system operation; it re-checks the link.
// System requests fail with ErrLinkMode until it succeeds, and again after
// Reset. Whether 'A' and FlagsExt0ForwardAllConfig are needed for system
// replies is unknown; they are sent because the original tools send them
// (reverse-engineered, #53).
func (c *Client) PrepareSystemLink(ctx context.Context) error {
	c.mu.Lock()
	c.linkReady = false
	c.mu.Unlock()
	v, err := c.Version(ctx)
	if err != nil {
		return err
	}
	if v.Mode == VersionModeRS485 {
		return ErrLinkMode
	}
	if err := c.SetAddress(ctx, 0); err != nil {
		return err
	}
	if err := c.SetState(ctx, systemLinkState); err != nil {
		return err
	}
	c.mu.Lock()
	c.linkReady = true
	c.mu.Unlock()
	return nil
}

// system sends a system frame. With a nil h it then keeps the line quiet
// for systemTiming.spacing; otherwise it waits up to Options.RawTimeout for
// the reply h accepts. c.mu must be held.
func (c *Client) system(ctx context.Context, payload []byte, h handler) error {
	if !c.linkReady {
		return ErrLinkMode
	}
	b, err := EncodeRaw(payload)
	if err != nil {
		return err
	}
	if h != nil {
		return c.exchangeBytes(ctx, b, c.opts.RawTimeout, h)
	}
	if err := c.exchangeBytes(ctx, b, 0, nil); err != nil {
		return err
	}
	return c.sleep(ctx, systemTiming.spacing)
}

// sendSystem sends a system frame that has no reply.
func (c *Client) sendSystem(ctx context.Context, longID uint32, sub byte, args ...byte) error {
	return c.do(func() error { return c.system(ctx, SystemPayload(longID, sub, args...), nil) })
}

// timeoutCause adds the reason to an ErrTimeout of a system request, if
// one is known: seen, the error of a frame that looked like the reply;
// else ErrChecksum if bytes arrived since mark (a value of c.skipped) that
// did not form a frame; else ErrFrameSize if a frame is still incomplete.
// The result still matches ErrTimeout. Bytes that did not form a frame are
// dropped, so that they cannot hide the next reply.
func (c *Client) timeoutCause(err error, mark int64, seen error) error {
	if !errors.Is(err, ErrTimeout) {
		return err
	}
	skipped := c.skipped.Load() > mark
	pending := c.received.Load() > max(c.consumed.Load(), c.discardTo.Load())
	if skipped || pending {
		c.discardReceived()
	}
	switch {
	case seen != nil:
		return fmt.Errorf("%w (%w)", err, seen)
	case skipped:
		return fmt.Errorf("%w (%w)", err, ErrChecksum)
	case pending:
		return fmt.Errorf("%w (%w)", err, ErrFrameSize)
	}
	return err
}

// SetIdentification switches identification on or off in the unit longID,
// or in every unit (LongIDAll). Only a unit with identification on answers
// Identify, so a scan switches a unit off once it has answered.
// Reverse-engineered (#53); the on/off meaning is INFERRED.
func (c *Client) SetIdentification(ctx context.Context, longID uint32, on bool) error {
	var arg byte
	if on {
		arg = 1
	}
	return c.sendSystem(ctx, longID, SysIdentifyMode, arg)
}

// Identify asks for the identity of the next unit with identification on.
// Like the original tools (reverse-engineered, #53), it first waits up to
// 500 ms for an identify reply that may already be queued or arriving, and
// only then broadcasts SysIdentify and waits Options.RawTimeout.
//
// A timeout is ErrTimeout; when a reason is known the error also matches
// ErrChecksum (garbled bytes), ErrFrameSize (incomplete frame),
// ErrShortReply (reply too short) or ErrUnexpectedReply (IdentifyFlagAlt
// without Options.AcceptIdentify11). Options.Retries applies.
func (c *Client) Identify(ctx context.Context) (Identity, error) {
	var id Identity
	err := c.do(func() error {
		if !c.linkReady {
			return ErrLinkMode
		}
		mark := c.skipped.Load()
		var seen error
		h := func(f Frame) (bool, bool, error) {
			p, err := ParseIdentify(f)
			switch {
			case errors.Is(err, ErrShortReply):
				seen = err
				return false, false, nil
			case err != nil:
				return false, false, nil
			case p.Flag == IdentifyFlagAlt && !c.opts.AcceptIdentify11:
				seen = fmt.Errorf("%w: identify flag 0x%02X", ErrUnexpectedReply, p.Flag)
				return false, false, nil
			}
			id = p
			return true, true, nil
		}
		if ok, err := c.listen(ctx, systemTiming.listen, h); ok || err != nil {
			return err
		}
		err := c.system(ctx, SystemPayload(LongIDAll, SysIdentify), h)
		return c.timeoutCause(err, mark, seen)
	})
	return id, err
}

// listen feeds frames that are queued or arrive within d to h, without
// sending anything, until h accepts one (true). Other frames go to the
// Unsolicited callback. c.mu must be held.
func (c *Client) listen(ctx context.Context, d time.Duration, h handler) (bool, error) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				return false, c.closedErr()
			}
			if mine, _, _ := h(f); mine {
				return true, nil
			}
			c.unsolicited(f)
		case <-timer.C:
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		case <-c.closing:
			return false, ErrClosed
		}
	}
}

// FlashLEDs starts or stops flashing the LEDs of the unit longID.
// Reverse-engineered (#53): "on" carries the fixed bytes 01 0F FF.
func (c *Client) FlashLEDs(ctx context.Context, longID uint32, on bool) error {
	if on {
		return c.sendSystem(ctx, longID, SysFlashLEDs, 0x01, 0x0F, 0xFF)
	}
	return c.sendSystem(ctx, longID, SysFlashLEDs, 0x00)
}

// ReadSystemConfig reads the system configuration record of the unit
// longID. Frames from other units and info frames are ignored. Errors are
// as for Identify; ErrUnexpectedReply means the unit answered with a
// response character outside the accepted range.
func (c *Client) ReadSystemConfig(ctx context.Context, longID uint32) (SystemConfig, error) {
	var sc SystemConfig
	err := c.do(func() error {
		mark := c.skipped.Load()
		var seen error
		err := c.system(ctx, SystemPayload(longID, SysReadConfig), func(f Frame) (bool, bool, error) {
			p, err := ParseSystemConfig(f, longID)
			if err != nil {
				if fromUnit(f, longID) {
					seen = err
				}
				return false, false, nil
			}
			sc = p
			return true, true, nil
		})
		return c.timeoutCause(err, mark, seen)
	})
	return sc, err
}

// fromUnit reports whether f is a FrameSystem frame that carries longID and
// is not an info frame (§6.1).
func fromUnit(f Frame, longID uint32) bool {
	d := f.Data
	return f.VEBus && f.Command == FrameSystem && len(d) >= 4 && binary.LittleEndian.Uint32(d) == longID &&
		(len(d) < 5 || d[4] < PhaseL4 || d[4] > PhaseDCFrame)
}

// WriteSystemConfig writes the system configuration record of the unit
// longID. There is no reply; read the record back to verify it. The new
// configuration takes effect after ResetSystem (INFERRED, #53).
func (c *Client) WriteSystemConfig(ctx context.Context, longID uint32, rec [SystemRecordLen]byte) error {
	return c.sendSystem(ctx, longID, SysWriteConfig, rec[:]...)
}

// ResetSystem restarts every unit on the bus (broadcast SysReset), so that
// written records take effect. All units switch off for a few seconds, so
// AC output is lost. There is no reply. The original tools reset with this
// frame, not with 'F' 8 (ResetDevice); whether the two are equivalent is
// unknown (reverse-engineered, #53).
func (c *Client) ResetSystem(ctx context.Context) error {
	return c.sendSystem(ctx, LongIDAll, SysReset)
}

// Freeze sends SysFreeze to the unit longID (or LongIDAll). Its effect is
// unknown (reverse-engineered, #53); front ends do not offer it.
func (c *Client) Freeze(ctx context.Context, longID uint32) error {
	return c.sendSystem(ctx, longID, SysFreeze)
}

// KeepAlive sends the keep-alive frame: broadcast SysKeepAlive 03 14.
// Reverse-engineered (#53): every original tool sends it every few seconds
// while the link is in VE.Bus mode. Its meaning is unknown; whether it is
// needed is a hardware check (#3).
func (c *Client) KeepAlive(ctx context.Context) error {
	return c.sendSystem(ctx, LongIDAll, SysKeepAlive, 0x03, 0x14)
}

// StartKeepAlive sends KeepAlive now and then every period until stop is
// called or the Client is closed. It goes through the Client like any
// request, so it never interrupts an exchange. Errors are ignored (while the
// link is not prepared it is ErrLinkMode and nothing is sent); the next
// period tries again. stop waits for a keep-alive in progress to finish.
//
// Sending it is optional: front ends let the user turn it off.
func (c *Client) StartKeepAlive(period time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(period)
		defer t.Stop()
		for {
			_ = c.KeepAlive(ctx)
			select {
			case <-t.C:
			case <-ctx.Done():
				return
			case <-c.closing:
				return
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
