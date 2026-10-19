package mk2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeLink is a serial link to a scripted MK2/MK3. Each end has its own baud
// rate; bytes sent while the rates differ are lost.
type fakeLink struct {
	mu   sync.Mutex
	cond *sync.Cond

	hostBaud, devBaud int
	in                []byte // device-to-host bytes not yet read
	closed            bool

	acceptN  bool // an MK3: 'N' switches the device to 115200
	replyN   bool // answer 'N' with an 'N' frame, already at 115200
	deafHigh bool // never answer 'V' at 115200
	dead     bool // never answer anything
	// garbage makes a frame the device sends at 115200 to a host at 2400
	// arrive as one 0xFF byte instead of being lost.
	garbage bool

	writes [][]byte // host writes, in order
	bauds  []int    // SetBaudRate calls, in order
}

func newFakeLink() *fakeLink {
	l := &fakeLink{hostBaud: BaudRate, devBaud: BaudRate}
	l.cond = sync.NewCond(&l.mu)
	return l
}

func (l *fakeLink) SetBaudRate(baud int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hostBaud = baud
	l.bauds = append(l.bauds, baud)
	return nil
}

func (l *fakeLink) Read(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for len(l.in) == 0 && !l.closed {
		l.cond.Wait()
	}
	if len(l.in) == 0 {
		return 0, io.EOF
	}
	n := copy(p, l.in)
	l.in = l.in[n:]
	return n, nil
}

func (l *fakeLink) Close() error {
	l.mu.Lock()
	l.closed = true
	l.cond.Broadcast()
	l.mu.Unlock()
	return nil
}

// Write delivers one host write to the device. The Client writes each frame
// and the autobaud string in a single call.
func (l *fakeLink) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, io.ErrClosedPipe
	}
	l.writes = append(l.writes, bytes.Clone(p))
	if l.hostBaud != l.devBaud || l.dead || bytes.Equal(p, autobaud) {
		return len(p), nil
	}
	f, err := Decode(p)
	if err != nil {
		return len(p), nil
	}
	switch f.Command {
	case CmdVersion:
		if !(l.deafHigh && l.devBaud == HighSpeedBaudRate) {
			l.send(Frame{Command: CmdVersion, Data: []byte{0x28, 0xDB, 0x11, 0x00, VersionModeNoAddress}})
		}
	case CmdBaudRate:
		if l.acceptN {
			l.devBaud = HighSpeedBaudRate
			if l.replyN {
				l.send(Frame{Command: CmdBaudRate})
			}
		}
	case CmdReset:
		l.devBaud = BaudRate
	}
	return len(p), nil
}

// send transmits f from the device at its current rate. l.mu must be held.
func (l *fakeLink) send(f Frame) {
	b, _ := f.Encode()
	switch {
	case l.hostBaud == l.devBaud:
		l.in = append(l.in, b...)
	case l.garbage && l.devBaud > l.hostBaud:
		l.in = append(l.in, 0xFF)
	default:
		return
	}
	l.cond.Broadcast()
}

func (l *fakeLink) state() (hostBaud, devBaud int, writes [][]byte, bauds []int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hostBaud, l.devBaud, slices.Clone(l.writes), slices.Clone(l.bauds)
}

func (l *fakeLink) wrote(b []byte) bool {
	_, _, writes, _ := l.state()
	return slices.ContainsFunc(writes, func(w []byte) bool { return bytes.Equal(w, b) })
}

var (
	frameN = []byte{0x02, 0xFF, 0x4E, 0xB1} // as sent by ve-ctrl-tool
	frameR = []byte{0x02, 0xFF, 0x52, 0xAD}
)

// startLink connects a Client to l with short timeouts.
func startLink(t *testing.T, l *fakeLink) *Client {
	t.Helper()
	saved := highSpeedTiming
	highSpeedTiming.beforeN = time.Millisecond
	highSpeedTiming.afterN = time.Millisecond
	highSpeedTiming.afterSync = 5 * time.Millisecond
	highSpeedTiming.afterReset = 5 * time.Millisecond
	t.Cleanup(func() { highSpeedTiming = saved })
	c := NewClient(l, &Options{Timeout: 50 * time.Millisecond})
	t.Cleanup(func() { c.Close() })
	return c
}

// checkLink asserts that the negotiated link works at baud.
func checkLink(t *testing.T, c *Client, l *fakeLink, baud int) {
	t.Helper()
	host, dev, _, _ := l.state()
	if host != baud || dev != baud {
		t.Fatalf("host %d, device %d baud, want %d", host, dev, baud)
	}
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("Version after negotiation: %v", err)
	}
}

func TestFrameNEncoding(t *testing.T) {
	b, err := Frame{Command: CmdBaudRate}.Encode()
	if err != nil || !bytes.Equal(b, frameN) {
		t.Fatalf("'N' = % X, %v; want % X", b, err, frameN)
	}
}

func TestNegotiateBaudHighSpeed(t *testing.T) {
	l := newFakeLink()
	l.acceptN = true
	// The 'N' reply arrives at 115200 while the host is still at 2400, so
	// the reader sees a stray 0xFF (a <Length> of 127) just before the
	// baud change. It must not swallow the 'V' reply at 115200.
	l.replyN = true
	l.garbage = true
	c := startLink(t, l)
	baud, err := c.NegotiateBaud(context.Background(), l, HighSpeedAuto)
	if err != nil || baud != HighSpeedBaudRate {
		t.Fatalf("NegotiateBaud = %d, %v; want %d, nil", baud, err, HighSpeedBaudRate)
	}
	checkLink(t, c, l, HighSpeedBaudRate)
	_, _, writes, bauds := l.state()
	iN := slices.IndexFunc(writes, func(w []byte) bool { return bytes.Equal(w, frameN) })
	if iN < 0 || iN+1 >= len(writes) || !bytes.Equal(writes[iN+1], autobaud) {
		t.Errorf("writes % X: want 'N' then UUUUU", writes)
	}
	if bauds[len(bauds)-1] != HighSpeedBaudRate {
		t.Errorf("bauds %v", bauds)
	}
}

func TestNegotiateBaudRefused(t *testing.T) {
	// An MK2, or an MK3 that ignores 'N': nothing answers at 115200.
	l := newFakeLink()
	c := startLink(t, l)
	baud, err := c.NegotiateBaud(context.Background(), l, HighSpeedAuto)
	if err != nil || baud != BaudRate {
		t.Fatalf("NegotiateBaud = %d, %v; want %d, nil", baud, err, BaudRate)
	}
	if !l.wrote(frameN) {
		t.Error("'N' not sent")
	}
	checkLink(t, c, l, BaudRate)
}

func TestNegotiateBaudVerifyFails(t *testing.T) {
	// The MK3 switches but never answers at 115200. The fallback must get
	// it back to 2400 (here only 'R' at 115200 does) without user action.
	l := newFakeLink()
	l.acceptN = true
	l.deafHigh = true
	c := startLink(t, l)
	baud, err := c.NegotiateBaud(context.Background(), l, HighSpeedAuto)
	if err != nil || baud != BaudRate {
		t.Fatalf("NegotiateBaud = %d, %v; want %d, nil", baud, err, BaudRate)
	}
	if !l.wrote(frameR) {
		t.Error("'R' not sent")
	}
	checkLink(t, c, l, BaudRate)
}

func TestNegotiateBaudForced2400(t *testing.T) {
	l := newFakeLink()
	l.acceptN = true
	c := startLink(t, l)
	baud, err := c.NegotiateBaud(context.Background(), l, HighSpeedOff)
	if err != nil || baud != BaudRate {
		t.Fatalf("NegotiateBaud = %d, %v; want %d, nil", baud, err, BaudRate)
	}
	_, _, writes, bauds := l.state()
	for _, w := range writes {
		if f, err := Decode(w); err == nil && f.Command == CmdBaudRate {
			t.Errorf("'N' sent with HighSpeedOff")
		}
	}
	if slices.Contains(bauds, HighSpeedBaudRate) {
		t.Errorf("bauds %v: port left 2400", bauds)
	}
	checkLink(t, c, l, BaudRate)
}

func TestNegotiateBaudForced2400RecoversStuckMK3(t *testing.T) {
	// The MK3 is still at 115200 from an earlier session.
	l := newFakeLink()
	l.devBaud = HighSpeedBaudRate
	l.acceptN = true
	c := startLink(t, l)
	baud, err := c.NegotiateBaud(context.Background(), l, HighSpeedOff)
	if err != nil || baud != BaudRate {
		t.Fatalf("NegotiateBaud = %d, %v; want %d, nil", baud, err, BaudRate)
	}
	if l.wrote(frameN) {
		t.Error("'N' sent with HighSpeedOff")
	}
	checkLink(t, c, l, BaudRate)
}

func TestNegotiateBaudNoLink(t *testing.T) {
	l := newFakeLink()
	l.dead = true
	c := startLink(t, l)
	baud, err := c.NegotiateBaud(context.Background(), l, HighSpeedAuto)
	if !errors.Is(err, ErrNoLink) || baud != BaudRate {
		t.Fatalf("NegotiateBaud = %d, %v; want %d, ErrNoLink", baud, err, BaudRate)
	}
	if l.wrote(frameN) {
		t.Error("'N' sent although nothing answered at 2400")
	}
	if host, _, _, _ := l.state(); host != BaudRate {
		t.Errorf("port left at %d", host)
	}
}

func TestNegotiateBaudContextCancel(t *testing.T) {
	l := newFakeLink()
	l.dead = true
	c := startLink(t, l)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.NegotiateBaud(ctx, l, HighSpeedAuto); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if host, _, _, _ := l.state(); host != BaudRate {
		t.Errorf("port left at %d", host)
	}
}
