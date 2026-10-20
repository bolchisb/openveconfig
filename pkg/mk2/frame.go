package mk2

import (
	"bytes"
	"errors"
)

// Marker is the byte that follows <Length> in every MK2 frame (§3.2).
// VE.Bus frames, as returned by 'F' (§6) or forwarded panel frames (§5.1),
// do not carry it.
const Marker = 0xFF

// LengthLEDFlag is the MSB of <Length>. When set, LED status is appended to
// the frame (§3.2, §5 <Flags[1]>).
const LengthLEDFlag = 0x80

// MaxBody is the largest value <Length> can hold: the 7 bits left after
// LengthLEDFlag.
const MaxBody = 0x7F

// Frame errors.
var (
	ErrChecksum   = errors.New("mk2: checksum mismatch")
	ErrShortFrame = errors.New("mk2: frame too short")
	ErrFrameSize  = errors.New("mk2: frame size does not match <Length>")
	ErrTooLong    = errors.New("mk2: frame too long")
)

// LEDStatus is the LED state reported by 'L' (§4) or appended to frames when
// LengthLEDFlag is set. Bit n of On/Blink is LED n (LEDMains...LEDTemperature).
type LEDStatus struct {
	On    byte
	Blink byte
}

// LED bit masks for LEDStatus.On and LEDStatus.Blink (§4).
const (
	LEDMains = 1 << iota
	LEDAbsorption
	LEDBulk
	LEDFloat
	LEDInverter
	LEDOverload
	LEDLowBattery
	LEDTemperature
)

// Frame is one decoded frame.
//
// An MK2 frame is <Length> 0xFF <Command> <Data...> <Checksum> (§3.2). A
// VE.Bus frame has the same layout without the 0xFF byte (§5.1, §6).
type Frame struct {
	// VEBus is true for frames without the 0xFF marker.
	VEBus bool
	// Command is the command (MK2 frames) or frame type (VE.Bus frames).
	Command byte
	// Data holds the bytes between Command and the checksum, excluding any
	// appended LED status.
	Data []byte
	// LED is non-nil when LED status is appended (MSB of <Length> set). It
	// occupies the last two bytes before the checksum (§3.2, Appendix 1).
	LED *LEDStatus
}

// Checksum returns the byte that makes the sum of b plus that byte equal 0
// modulo 256 (§3.2). b is the frame without its checksum.
func Checksum(b []byte) byte {
	var sum byte
	for _, c := range b {
		sum += c
	}
	return -sum
}

// Encode returns the wire form of f, including <Length> and <Checksum>.
func (f Frame) Encode() ([]byte, error) {
	n := 1 + len(f.Data)
	if !f.VEBus {
		n++
	}
	if f.LED != nil {
		n += 2
	}
	if n > MaxBody {
		return nil, ErrTooLong
	}
	b := make([]byte, 0, n+2)
	length := byte(n)
	if f.LED != nil {
		length |= LengthLEDFlag
	}
	b = append(b, length)
	if !f.VEBus {
		b = append(b, Marker)
	}
	b = append(b, f.Command)
	b = append(b, f.Data...)
	if f.LED != nil {
		b = append(b, f.LED.On, f.LED.Blink)
	}
	return append(b, Checksum(b)), nil
}

// Decode parses exactly one frame, b, including <Length> and <Checksum>.
// A frame whose second byte is 0xFF is an MK2 frame; anything else is a
// VE.Bus frame. The returned Data is a copy of b's contents.
//
// Decode does not interpret Data, so frames longer than a command expects
// (§3.2: "ignore all non-documented superfluous data") decode fine; the
// command parsers ignore the extra bytes.
func Decode(b []byte) (Frame, error) {
	if len(b) < 3 {
		return Frame{}, ErrShortFrame
	}
	n := int(b[0] &^ LengthLEDFlag)
	if n == 0 {
		return Frame{}, ErrShortFrame
	}
	if len(b) != n+2 {
		return Frame{}, ErrFrameSize
	}
	var sum byte
	for _, c := range b {
		sum += c
	}
	if sum != 0 {
		return Frame{}, ErrChecksum
	}
	body := b[1 : n+1]
	var f Frame
	if body[0] == Marker {
		if len(body) < 2 {
			return Frame{}, ErrShortFrame
		}
		body = body[1:]
	} else {
		f.VEBus = true
	}
	f.Command = body[0]
	data := body[1:]
	if b[0]&LengthLEDFlag != 0 {
		if len(data) < 2 {
			return Frame{}, ErrShortFrame
		}
		f.LED = &LEDStatus{On: data[len(data)-2], Blink: data[len(data)-1]}
		data = data[:len(data)-2]
	}
	f.Data = bytes.Clone(data)
	if f.Data == nil {
		f.Data = []byte{}
	}
	return f, nil
}

// ScanFrames is a bufio.SplitFunc that splits a byte stream into frames, MK2
// or VE.Bus. Use it on the device-to-host direction. Bytes that do not start
// a frame with a valid checksum are skipped one at a time, so the scanner
// resynchronises after line noise. Tokens are complete frames for Decode.
//
// A noise byte can look like the <Length> of a frame that has not fully
// arrived yet (after a corrupt MK2 frame, its 0xFF marker reads as a
// <Length> of 127). The scanner does not wait for the rest if an anchor
// already follows in the buffer; it skips ahead to it. An anchor is a
// complete MK2 frame (0xFF marker) with a valid checksum, or a complete
// VE.Bus frame with a valid checksum that is directly followed by another
// complete, valid frame. A single VE.Bus-shaped run with a valid checksum
// turns up too easily (about 1 in 256 per candidate) inside the data of a
// genuine frame that is still arriving, so it does not count on its own.
func ScanFrames(data []byte, atEOF bool) (advance int, token []byte, err error) {
	return scan(data, atEOF, scanAny)
}

// ScanMK2Frames is like ScanFrames but only accepts MK2 frames (second byte
// 0xFF). Use it on the host-to-device direction, where every frame is an MK2
// frame; it also skips sync bytes such as 0x55 that some hosts send.
func ScanMK2Frames(data []byte, atEOF bool) (advance int, token []byte, err error) {
	return scan(data, atEOF, scanMK2)
}

// ScanHostFrames is like ScanMK2Frames but also accepts raw frames (see
// EncodeRaw): a frame whose second byte is not 0xFF is checked with
// RawChecksum. Use it on the host-to-device direction when the host sends
// system frames. In the anchor rule of ScanFrames, raw frames take the place
// of VE.Bus frames.
func ScanHostFrames(data []byte, atEOF bool) (advance int, token []byte, err error) {
	return scan(data, atEOF, scanHost)
}

// scanMode selects the frames scan accepts.
type scanMode int

const (
	scanAny  scanMode = iota // MK2 and VE.Bus frames
	scanMK2                  // MK2 frames only
	scanHost                 // MK2 frames and raw frames
)

func scan(data []byte, atEOF bool, mode scanMode) (int, []byte, error) {
	anchor := -1 // offset of an anchor after an incomplete candidate
	for start := 0; ; start++ {
		rest := data[start:]
		if len(rest) < 3 {
			if atEOF {
				return len(data), nil, nil
			}
			return start, nil, nil
		}
		n := bodyLen(rest, mode)
		if n == 0 {
			continue
		}
		if len(rest) < n+2 {
			if atEOF || start < anchor {
				continue
			}
			if anchor = nextAnchor(data, start+1, mode); anchor < 0 {
				return start, nil, nil
			}
			continue
		}
		if frameOK(rest[:n+2], mode) {
			return start + n + 2, rest[:n+2], nil
		}
	}
}

// bodyLen returns the <Length> of a frame accepted in this mode that may
// start at b, or 0 if none can. len(b) >= 2.
func bodyLen(b []byte, mode scanMode) int {
	n := int(b[0] &^ LengthLEDFlag)
	switch {
	case mode == scanHost && b[1] != Marker:
		// A raw frame: <n> counts the payload and has no LED flag.
		if b[0] > MaxBody {
			return 0
		}
		return int(b[0])
	case mode != scanAny && n < 2, mode == scanMK2 && b[1] != Marker:
		return 0
	}
	return n
}

// frameOK reports whether the complete frame b has a valid checksum: the
// raw rule for a raw frame in host mode, otherwise a byte sum of 0.
func frameOK(b []byte, mode scanMode) bool {
	if mode == scanHost && b[1] != Marker {
		return b[len(b)-1] == RawChecksum(b[1:len(b)-1])
	}
	return checksumOK(b)
}

// nextAnchor returns the offset of the first anchor (see ScanFrames) in
// data at or after from, or -1. scan only calls it while a candidate frame
// of at most MaxBody+2 bytes is incomplete, so data[from:] is shorter than
// that and the search is bounded.
func nextAnchor(data []byte, from int, mode scanMode) int {
	for j := from; j < len(data); j++ {
		n := frameAt(data, j, mode)
		if n >= 4 && data[j+1] == Marker || n > 0 && frameAt(data, j+n, mode) > 0 {
			return j
		}
	}
	return -1
}

// frameAt returns the size of the complete frame with a valid checksum at
// data[j:], or 0. In host mode the frame is an MK2 or raw frame; otherwise
// it is an MK2 or VE.Bus frame.
func frameAt(data []byte, j int, mode scanMode) int {
	if j+3 > len(data) {
		return 0
	}
	n := int(data[j]&^LengthLEDFlag) + 2
	if mode == scanHost {
		n = bodyLen(data[j:], mode) + 2
	}
	if n == 2 || j+n > len(data) || !frameOK(data[j:j+n], mode) {
		return 0
	}
	return n
}

func checksumOK(b []byte) bool {
	var sum byte
	for _, c := range b {
		sum += c
	}
	return sum == 0
}
