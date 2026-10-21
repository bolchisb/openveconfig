package firmware

import (
	"errors"
	"fmt"
)

// FrameError is a received frame that failed validation: a wrong checksum, a
// wrong length, or a leading byte that is not an expected acknowledgement.
type FrameError struct {
	Reason string
}

func (e *FrameError) Error() string { return "firmware: bad frame: " + e.Reason }

// ErrTargetRefused is returned when the target answers with the failure code
// ('F') instead of an acknowledgement.
var ErrTargetRefused = errors.New("firmware: target refused the frame (F)")

// checksum is the 8-bit additive checksum of payload, negated so that
// payload followed by the checksum byte sums to 0 (mod 256).
// reverse-engineered (#12).
func checksum(payload []byte) byte {
	var s byte
	for _, b := range payload {
		s += b
	}
	return -s // 0 - sum, mod 256
}

// escape inserts escapeByte after every 0x00 byte (NUL-escape).
// reverse-engineered (#12).
func escape(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		out = append(out, c)
		if c == 0x00 {
			out = append(out, escapeByte)
		}
	}
	return out
}

// unescape removes the byte inserted after every 0x00 byte and returns the
// original bytes. It errors if a 0x00 is the last byte (no stuffing byte).
func unescape(b []byte) ([]byte, error) {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		out = append(out, b[i])
		if b[i] == 0x00 {
			i++ // skip the inserted stuffing byte
			if i >= len(b) {
				return nil, &FrameError{"escape sequence cut short"}
			}
		}
	}
	return out, nil
}

// buildFrame wraps payload in the start-of-frame framing for transmission:
// it appends the checksum, NUL-escapes the payload+checksum, then prepends
// the start-of-frame byte and a length byte. reverse-engineered (#12).
func buildFrame(payload []byte) ([]byte, error) {
	withCks := append(append([]byte(nil), payload...), checksum(payload))
	n := len(withCks) // length before escaping
	if n < 1 || n > 0x80 {
		return nil, fmt.Errorf("firmware: frame payload length %d out of range", len(payload))
	}
	esc := escape(withCks)
	frame := make([]byte, 0, len(esc)+2)
	frame = append(frame, sof, byte((n-1)|lenHighBit))
	return append(frame, esc...), nil
}

// buildAck builds a response (acknowledgement) frame in the plain length
// framing: a length byte L (the payload length) then L+1 bytes, the payload
// and a checksum that makes payload+checksum sum to 0. reverse-engineered
// (#12). The bootloader fake uses it to answer; the host reads it with
// readAck.
func buildAck(payload []byte) []byte {
	frame := make([]byte, 0, len(payload)+2)
	frame = append(frame, byte(len(payload)))
	frame = append(frame, payload...)
	return append(frame, checksum(payload))
}

// validateAck checks a response payload+checksum: the whole must sum to 0
// (mod 256) and the first byte must be an expected acknowledgement. 'F' is
// reported as ErrTargetRefused. expect lists the accepted leading bytes; an
// empty expect accepts any non-'F' code. buf is payload followed by the
// checksum byte.
func validateAck(buf []byte, expect ...byte) error {
	if len(buf) < 2 {
		return &FrameError{"acknowledgement too short"}
	}
	var s byte
	for _, b := range buf {
		s += b
	}
	if s != 0 {
		return &FrameError{fmt.Sprintf("checksum: payload+checksum sums to %#02x, not 0", s)}
	}
	lead := buf[0]
	if lead == ackFail {
		return ErrTargetRefused
	}
	if len(expect) == 0 {
		return nil
	}
	for _, e := range expect {
		if lead == e {
			return nil
		}
	}
	return &FrameError{fmt.Sprintf("unexpected acknowledgement %#02x", lead)}
}
