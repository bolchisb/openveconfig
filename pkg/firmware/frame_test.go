package firmware

import (
	"bytes"
	"errors"
	"testing"
)

func TestChecksumSumsToZero(t *testing.T) {
	for _, payload := range [][]byte{
		{},
		{0x01},
		{0x00, 0x00, 0x00},
		{'V'},
		bytes.Repeat([]byte{0xFF}, 70),
		{0x12, 0x34, 0x56, 0x78, 0x9A},
	} {
		cks := checksum(payload)
		var s byte
		for _, b := range payload {
			s += b
		}
		s += cks
		if s != 0 {
			t.Errorf("payload %X: payload+checksum sums to %#02x, not 0", payload, s)
		}
	}
}

func TestEscapeRoundTrip(t *testing.T) {
	for _, b := range [][]byte{
		{},
		{0x00},
		{0x00, 0x00},
		{0x01, 0x00, 0x02, 0x00, 0x00, 0x03},
		bytes.Repeat([]byte{0x00}, 10),
		{0x55, 0x80, 0x01, 0x00, 0x80},
	} {
		esc := escape(b)
		got, err := unescape(esc)
		if err != nil {
			t.Fatalf("%X: unescape: %v", b, err)
		}
		if !bytes.Equal(got, b) {
			t.Errorf("%X: round trip got %X", b, got)
		}
		// Every 0x00 must be followed by the stuffing byte.
		for i := 0; i < len(esc); i++ {
			if esc[i] == 0x00 {
				if i+1 >= len(esc) || esc[i+1] != escapeByte {
					t.Errorf("%X: 0x00 at %d not followed by escape byte", b, i)
				}
				i++
			}
		}
	}
}

func TestBuildFrameStructure(t *testing.T) {
	payload := []byte{0x40, 0x01, 0x00} // a byte that needs escaping
	frame, err := buildFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	if frame[0] != sof {
		t.Errorf("start-of-frame = %#02x", frame[0])
	}
	n := len(payload) + 1 // payload + checksum, before escaping
	if frame[1] != byte((n-1)|lenHighBit) {
		t.Errorf("length byte = %#02x, want %#02x", frame[1], byte((n-1)|lenHighBit))
	}
	// Decode: strip header, unescape, the body must sum to 0.
	body, err := unescape(frame[2:])
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != n {
		t.Fatalf("decoded body length %d, want %d", len(body), n)
	}
	var s byte
	for _, b := range body {
		s += b
	}
	if s != 0 {
		t.Errorf("decoded body sums to %#02x, not 0", s)
	}
}

func TestValidateAck(t *testing.T) {
	good := buildAck([]byte{ackAlive})
	// good is [L]['V'][cks]; validateAck takes payload+checksum (skip L).
	if err := validateAck(good[1:], ackAlive); err != nil {
		t.Errorf("good ack rejected: %v", err)
	}
	if err := validateAck(good[1:], ackReady); err == nil {
		t.Error("ack with wrong expected code accepted")
	}
	// Failure code.
	fail := buildAck([]byte{ackFail})
	if err := validateAck(fail[1:]); !errors.Is(err, ErrTargetRefused) {
		t.Errorf("fail code: err = %v, want ErrTargetRefused", err)
	}
	// Corrupted checksum.
	bad := append([]byte(nil), good[1:]...)
	bad[len(bad)-1] ^= 0xFF
	var fe *FrameError
	if err := validateAck(bad, ackAlive); !errors.As(err, &fe) {
		t.Errorf("corrupt checksum: err = %v, want *FrameError", err)
	}
}
