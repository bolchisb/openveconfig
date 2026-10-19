package mk2

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// device is a scripted MK2 on the far end of a net.Pipe.
type device struct {
	t    *testing.T
	conn net.Conn

	mu   sync.Mutex
	reqs [][]byte // raw request frames, in order
}

// startDevice connects a Client to a device whose handler is called for
// every request frame the Client sends.
func startDevice(t *testing.T, opts *Options, handle func(d *device, req Frame)) (*Client, *device) {
	t.Helper()
	host, dev := net.Pipe()
	d := &device{t: t, conn: dev}
	go func() {
		sc := bufio.NewScanner(dev)
		sc.Split(ScanMK2Frames)
		for sc.Scan() {
			raw := bytes.Clone(sc.Bytes())
			d.mu.Lock()
			d.reqs = append(d.reqs, raw)
			d.mu.Unlock()
			f, err := Decode(raw)
			if err != nil {
				t.Errorf("device: bad request % X: %v", raw, err)
				continue
			}
			if handle != nil {
				handle(d, f)
			}
		}
		dev.Close()
	}()
	if opts == nil {
		opts = &Options{}
	}
	if opts.Timeout == 0 {
		opts.Timeout = 200 * time.Millisecond
	}
	c := NewClient(host, opts)
	t.Cleanup(func() { c.Close() })
	return c, d
}

// send writes raw frames to the Client.
func (d *device) send(frames ...[]byte) {
	for _, b := range frames {
		if _, err := d.conn.Write(b); err != nil {
			return // client closed
		}
	}
}

// sendFrame encodes and writes f.
func (d *device) sendFrame(f Frame) {
	b, err := f.Encode()
	if err != nil {
		d.t.Errorf("device: %v", err)
		return
	}
	d.send(b)
}

func (d *device) requests() [][]byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([][]byte(nil), d.reqs...)
}

func wReply(p ...byte) Frame { return Frame{Command: 'W', Data: p} }

func TestUnsolicitedFramesInterleaved(t *testing.T) {
	var mu sync.Mutex
	var unsolicited []Frame
	opts := &Options{Unsolicited: func(f Frame) {
		mu.Lock()
		unsolicited = append(unsolicited, f)
		mu.Unlock()
	}}
	c, _ := startDevice(t, opts, func(d *device, req Frame) {
		// Unsolicited 'V' and a forwarded panel frame arrive before the reply.
		d.send(withCS(t, "07 FF 'V 93 3E 11 00 00"))
		d.sendFrame(Frame{VEBus: true, Command: FramePanel, Data: []byte{0x03, 0xC0, 0x10}})
		d.sendFrame(wReply(WReplyReadSettingOK, 0x40, 0x0B))
		d.send(withCS(t, "07 FF 'V 93 3E 11 00 00"))
	})
	v, err := c.ReadSetting(context.Background(), 2)
	if err != nil || v != 0x0B40 {
		t.Fatalf("ReadSetting = %#x, %v", v, err)
	}
	// The trailing 'V' is delivered with the next request.
	if _, err := c.ReadSetting(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(unsolicited) < 3 || unsolicited[0].Command != 'V' || unsolicited[1].Command != FramePanel {
		t.Fatalf("unsolicited = %+v", unsolicited)
	}
	if ver, err := ParseVersion(unsolicited[0]); err != nil || ver.Version != 1130131 {
		t.Fatalf("ParseVersion = %+v, %v", ver, err)
	}
}

func TestTimeoutAndRetries(t *testing.T) {
	// The device answers every second request.
	handler := func() func(d *device, req Frame) {
		n := 0
		return func(d *device, req Frame) {
			n++
			if n%2 == 0 {
				d.sendFrame(Frame{Command: 'L', Data: []byte{1, 2}})
			}
		}
	}
	c, _ := startDevice(t, &Options{Timeout: 30 * time.Millisecond}, handler())
	if _, err := c.LED(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("no retry: err = %v", err)
	}
	c, d := startDevice(t, &Options{Timeout: 30 * time.Millisecond, Retries: 1}, handler())
	if s, err := c.LED(context.Background()); err != nil || s != (LEDStatus{1, 2}) {
		t.Fatalf("with retry: %+v, %v", s, err)
	}
	if len(d.requests()) != 2 {
		t.Fatalf("requests = %d", len(d.requests()))
	}
}

func TestSlowTimeoutForMasterMultiLED(t *testing.T) {
	opts := &Options{Timeout: 20 * time.Millisecond, SlowTimeout: 500 * time.Millisecond}
	c, _ := startDevice(t, opts, func(d *device, req Frame) {
		time.Sleep(60 * time.Millisecond)
		switch req.Data[0] {
		case FTypeMasterMultiLED:
			d.send(withCS(t, "0C 41 10 09 00 00 00 3E 00 E8 03 F4 01"))
		case FTypeDC:
			d.send(withCS(t, "0F 20 7F 9A 81 79 0C 51 0A 00 00 00 00 00 00 88"))
		}
	})
	if _, err := c.MasterMultiLED(context.Background()); err != nil {
		t.Fatalf("F 5 with slow timeout: %v", err)
	}
	if _, err := c.DCInfo(context.Background()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("F 0 must use the normal timeout: %v", err)
	}
}

func TestContextCancel(t *testing.T) {
	c, _ := startDevice(t, &Options{Timeout: time.Second}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.LED(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestClose(t *testing.T) {
	c, _ := startDevice(t, nil, nil)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LED(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("after Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestTransportEOF(t *testing.T) {
	r, w := io.Pipe()
	c := NewClient(struct {
		io.Reader
		io.Writer
	}{r, io.Discard}, nil)
	defer c.Close()
	w.Close()
	if _, err := c.LED(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v", err)
	}
}

func TestTaggedTunnel(t *testing.T) {
	c, d := startDevice(t, &Options{Tunnel: TunnelXYZ}, func(d *device, req Frame) {
		r, err := ParseWinmonRequest(req)
		if err != nil {
			t.Error(err)
			return
		}
		if r.Tag == 'Y' {
			// A late reply to the 'X' request must be ignored.
			d.sendFrame(Frame{Command: 'X', Data: []byte{WReplyReadSettingOK, 0xEE, 0xEE}})
		}
		d.sendFrame(Frame{Command: r.Tag, Data: []byte{WReplyReadSettingOK, r.Info[0], 0}})
	})
	ctx := context.Background()
	for i := uint16(1); i <= 4; i++ {
		v, err := c.ReadSetting(ctx, i)
		if err != nil || v != i {
			t.Fatalf("ReadSetting(%d) = %d, %v", i, v, err)
		}
	}
	var tags []byte
	for _, r := range d.requests() {
		tags = append(tags, r[2])
	}
	if string(tags) != "XYZX" {
		t.Fatalf("tags = %q", tags)
	}
}

func TestAddressedTunnel(t *testing.T) {
	c, d := startDevice(t, &Options{Tunnel: TunnelXYZAddressed}, func(d *device, req Frame) {
		r, err := ParseWinmonRequest(req)
		if err != nil {
			t.Error(err)
			return
		}
		// Replies are always uppercase (§7.3).
		d.sendFrame(Frame{Command: ReplyTag(r.Tag), Data: []byte{WReplyReadSettingOK, r.Dest, 0}})
	})
	c.SetDestination(3)
	v, err := c.ReadSetting(context.Background(), 0x0102)
	if err != nil || v != 3 {
		t.Fatalf("ReadSetting = %d, %v", v, err)
	}
	if want := withCS(t, "06 FF 'x 31 02 01 03"); !bytes.Equal(d.requests()[0], want) {
		t.Fatalf("request = % X, want % X", d.requests()[0], want)
	}
}

func TestWinmonRequestRoundTrip(t *testing.T) {
	for _, r := range []WinmonRequest{
		{Tag: 'W', Cmd: 0x31, Info: []byte{1, 0}},
		{Tag: 'z', Cmd: 0x37, Info: []byte{1, 2, 3, 4}, Dest: 5},
	} {
		got, err := ParseWinmonRequest(r.Frame())
		if err != nil || got.Tag != r.Tag || got.Cmd != r.Cmd || !bytes.Equal(got.Info, r.Info) || got.Dest != r.Dest {
			t.Errorf("round trip %+v -> %+v, %v", r, got, err)
		}
	}
	if _, err := ParseWinmonRequest(Frame{Command: 'L'}); err == nil {
		t.Error("'L' is not a Winmon request")
	}
	if _, err := ParseWinmonRequest(Frame{Command: 'x', Data: []byte{1}}); err == nil {
		t.Error("short addressed request must fail")
	}
}
