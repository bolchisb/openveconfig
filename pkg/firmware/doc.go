// Package firmware reprograms the firmware of a VE.Bus device over the
// bootloader transport of its MK2/MK3 interface (#38, epic #46). It builds
// the record stream from a *vff.File (#37), drives the bootloader handshake
// and the erase/program phases, and checks a file against a target before
// anything is written.
//
// SAFETY. Writing firmware to real hardware can brick it and is not yet
// validated on real hardware. Every write path is exercised against the
// in-package bootloader fake (BootSim) only. The path that writes to a real
// (non-simulator) serial port is gated off by default and returns
// ErrNotHardwareValidated unless the caller opts in explicitly; see
// CheckHardwareGate. Do not enable the real-hardware path in any test or
// default code path.
//
// Protocol. Victron has published no description of the bootloader
// transport. The framing and record layout below are reverse-engineered
// (#12) and restated in our own words; each wire constant is marked at its
// definition. The transport is NOT the normal MK2 frame protocol
// (pkg/mk2): it has its own start-of-frame framing and runs over the raw
// serial line. The device firmware number read during pre-flight is read
// with the normal MK2 protocol (pkg/mk2.Client) before the device is put
// into its bootloader.
//
// What is implemented here is the transport, record and state-machine layer
// behind the simulator. Enabling real device writes additionally needs a
// byte-for-byte capture of one real session matched against the spec; until
// then the gate stays closed.
package firmware

// Wire constants of the bootloader transport. // reverse-engineered (#12)
//
// A sent record frame is: a start-of-frame byte, a length byte, then the
// NUL-escaped payload with a trailing checksum byte. The checksum is an
// 8-bit additive sum (init 0, mod 256) negated so that payload+checksum
// sums to 0. A received acknowledgement frame is a plain length byte then
// length+1 bytes (payload + checksum) that sum to 0; its first payload byte
// classifies the outcome.
const (
	// sof marks the start of a sent frame. // reverse-engineered (#12)
	sof = 0x01
	// lenHighBit is set in a sent frame's length byte; the low 7 bits hold
	// (N-1) where N is the payload+checksum length before escaping.
	// reverse-engineered (#12)
	lenHighBit = 0x80
	// escapeByte is inserted right after every 0x00 byte of a sent frame's
	// payload+checksum, and removed on receipt (NUL-escape).
	// reverse-engineered (#12)
	escapeByte = 0x80
	// dataBlockLen is the size of one programmed data block.
	// reverse-engineered (#12)
	dataBlockLen = 0x40 // 64
	// autobaudByte is written repeatedly to let the bootloader lock onto the
	// line speed, and autobaudCount is how many are sent.
	// reverse-engineered (#12)
	autobaudByte  = 0x55 // 'U'
	autobaudCount = 12
)

// Acknowledgement codes: the first payload byte of a response frame (and the
// single byte returned after the autobaud sync). // reverse-engineered (#12)
const (
	ackAlive = 'V' // target is alive / a block was accepted
	ackReady = 'R' // target is ready
	ackFail  = 'F' // target reported a failure
)

// Record type codes distinguish the kind of a data record on the wire. The
// original uses a small code (0..3); these are our names for the kinds we
// stream. // reverse-engineered (#12)
const (
	typeMainCode  byte = 0 // a block of main firmware code
	typeEEPROM    byte = 1 // a block of EEPROM data
	typeBootblock byte = 2 // a block of bootblock code
)

// Command-frame bytes. The original builds a command byte from a base plus
// flag bits; the exact bit meanings are only partially recovered (#12,
// Unknown 3), so the values below are ours for driving the simulator and are
// not confirmed against hardware.
//
// TODO(#38): confirm the command byte and its flag bits, the set-value /
// set-address tokens and the per-connection-method entry choreography from a
// real capture before the real-hardware gate is opened.
const (
	cmdBase    = 0x40 // '@': base command byte (no firmware descriptor attached)
	cmdErase   = cmdBase | 0x01
	cmdExecute = cmdBase | 0x08
)

// TODO(#38): a second, plainer TX framing ([length] then length+1 bytes,
// same additive checksum) is selected by the file's connection-method byte
// for one entry method. Only the start-of-frame framing above is
// implemented; the simulator does not need the alternate, so it is left for
// the hardware-validation follow-up.
