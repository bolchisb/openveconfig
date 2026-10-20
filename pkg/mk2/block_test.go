package mk2

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"
)

// testCodec is a made-up block codec for these tests only: read 0xF0 n,
// reply 0xF1 n data; write 0xF2 n data, reply 0xF3 n; refusal 0xF4 n;
// out of range 0xF5 n. Block size 4.
type testCodec struct{}

func (testCodec) Hardware() bool { return false }
func (testCodec) BlockSize() int { return 4 }

func (testCodec) ReadRequest(n int) (byte, []byte, error) { return 0xF0, le16(uint16(n)), nil }

func (testCodec) ParseRead(n int, p []byte) ([]byte, bool, error) {
	if len(p) < 3 || binary.LittleEndian.Uint16(p[1:]) != uint16(n) {
		return nil, false, nil
	}
	switch p[0] {
	case 0xF1:
		return p[3:], true, nil
	case 0xF5:
		return nil, true, ErrBlockRange
	}
	return nil, false, nil
}

func (testCodec) WriteRequest(n int, data []byte) (byte, []byte, error) {
	return 0xF2, append(le16(uint16(n)), data...), nil
}

func (testCodec) ParseWrite(n int, p []byte) (bool, error) {
	if len(p) < 3 || binary.LittleEndian.Uint16(p[1:]) != uint16(n) {
		return false, nil
	}
	switch p[0] {
	case 0xF3:
		return true, nil
	case 0xF4:
		return true, ErrBlockRefused
	case 0xF5:
		return true, ErrBlockRange
	}
	return false, nil
}

func TestBlockReadWrite(t *testing.T) {
	mem := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		cmd, n := req.Data[0], int(binary.LittleEndian.Uint16(req.Data[1:]))
		reply := func(code byte, data ...byte) {
			d.sendFrame(wReply(append([]byte{code, byte(n), byte(n >> 8)}, data...)...))
		}
		// A reply about another block is not taken.
		d.sendFrame(wReply(0xF1, byte(n+1), byte((n+1)>>8), 9, 9, 9, 9))
		switch {
		case cmd == 0xF0 && n < 2:
			reply(0xF1, mem[4*n:4*n+4]...)
		case cmd == 0xF2 && n == 1:
			reply(0xF4)
		case cmd == 0xF2 && n < 2:
			copy(mem[4*n:], req.Data[3:])
			reply(0xF3)
		default:
			reply(0xF5)
		}
	})
	ctx := context.Background()
	if b, err := c.ReadBlock(ctx, testCodec{}, 1); err != nil || !bytes.Equal(b, []byte{5, 6, 7, 8}) {
		t.Fatalf("ReadBlock(1) = % X, %v", b, err)
	}
	if !bytes.Equal(d.requests()[0], withCS(t, "05 FF 'W F0 01 00")) {
		t.Errorf("read request = % X", d.requests()[0])
	}
	if _, err := c.ReadBlock(ctx, testCodec{}, 2); !errors.Is(err, ErrBlockRange) {
		t.Errorf("ReadBlock(2): %v, want ErrBlockRange", err)
	}
	if err := c.WriteBlock(ctx, testCodec{}, 0, []byte{0xA, 0xB, 0xC, 0xD}); err != nil || mem[0] != 0xA {
		t.Errorf("WriteBlock(0): %v, mem % X", err, mem)
	}
	if err := c.WriteBlock(ctx, testCodec{}, 1, []byte{0, 0, 0, 0}); !errors.Is(err, ErrBlockRefused) || mem[4] != 5 {
		t.Errorf("WriteBlock(1): %v, mem % X", err, mem)
	}
}

func TestBlockUnknownCommand(t *testing.T) {
	c, _ := startDevice(t, nil, func(d *device, req Frame) {
		d.sendFrame(wReply(WReplyUnknownCommand, 0, 0))
	})
	var uc *UnknownCommandError
	if _, err := c.ReadBlock(context.Background(), testCodec{}, 0); !errors.As(err, &uc) {
		t.Fatalf("ReadBlock: %v, want *UnknownCommandError", err)
	}
}

// TestDeviceBlockCodecSendsNothing: the codec of real devices sends no
// request at all, read or write.
func TestDeviceBlockCodecSendsNothing(t *testing.T) {
	c, d := startDevice(t, &Options{Timeout: 50 * time.Millisecond}, nil)
	ctx := context.Background()
	codec := DeviceBlockCodec{}
	if !codec.Hardware() || codec.BlockSize() != 0 {
		t.Errorf("Hardware %v, BlockSize %d", codec.Hardware(), codec.BlockSize())
	}
	if _, err := c.ReadBlock(ctx, codec, 0); !errors.Is(err, ErrBlockTransferUnspecified) {
		t.Errorf("ReadBlock: %v", err)
	}
	if err := c.WriteBlock(ctx, codec, 0, make([]byte, 64)); !errors.Is(err, ErrBlockTransferUnspecified) {
		t.Errorf("WriteBlock: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if r := d.requests(); len(r) != 0 {
		t.Fatalf("sent % X", r)
	}
}

func TestProbeVLLBlockRead(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		if req.Data[1] == 0xFF {
			d.sendFrame(wReply(WReplyUnknownCommand, 1, 0))
			return
		}
		d.sendFrame(wReply(0xA0, 0x11, 0x22))
	})
	ctx := context.Background()
	p, err := c.ProbeVLLBlockRead(ctx, 0x0102)
	if err != nil || !bytes.Equal(p, []byte{0xA0, 0x11, 0x22}) {
		t.Fatalf("probe = % X, %v", p, err)
	}
	if !bytes.Equal(d.requests()[0], withCS(t, "05 FF 'W 5F 02 01")) {
		t.Errorf("request = % X", d.requests()[0])
	}
	var uc *UnknownCommandError
	if _, err := c.ProbeVLLBlockRead(ctx, 0xFF); !errors.As(err, &uc) || uc.Reason != 1 {
		t.Errorf("unknown: %v", err)
	}
}
