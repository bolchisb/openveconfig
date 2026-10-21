package firmware

import (
	"encoding/binary"
	"io"
	"sync"
)

// BootConfig configures a BootSim.
type BootConfig struct {
	// InterruptAfter, if > 0, makes the fake stop acknowledging after this
	// many record frames in a stream, to simulate an interrupted update. The
	// host then times out (ErrNoResponse).
	InterruptAfter int
	// InterruptOnce makes InterruptAfter apply only to the first stream; a
	// later stream (after a fresh autobaud sync) completes, which models the
	// "just restart the update" recovery (spec Q5).
	InterruptOnce bool
}

// BootSim is a fake VE.Bus bootloader target for tests and the --simulator
// path of the CLI. It speaks the bootloader transport only (not MK2): it
// answers the autobaud sync, accepts start-of-frame record and command
// frames, verifies their checksums, acknowledges them, and records the
// indices of the blocks it was sent. It is safe for concurrent use and is
// read like a serial port (Read blocks until there is something to return).
//
// It is a test/simulator fake, not a model of real bootloader behaviour.
type BootSim struct {
	cfg BootConfig

	mu      sync.Mutex
	cond    *sync.Cond
	in      []byte // host bytes not yet forming a frame
	out     []byte // bytes waiting for Read
	closed  bool
	acked   int // record frames seen in the current stream
	stream  int // how many streams (autobaud syncs) have started
	written []uint16
}

// NewBootSim returns a running BootSim.
func NewBootSim(cfg BootConfig) *BootSim {
	b := &BootSim{cfg: cfg}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Written returns the block indices acknowledged in the most recent stream,
// in order.
func (b *BootSim) Written() []uint16 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]uint16(nil), b.written...)
}

// Read returns bytes queued by the fake, blocking until some are available
// or the fake is closed (then io.EOF).
func (b *BootSim) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.out) == 0 && !b.closed {
		b.cond.Wait()
	}
	if b.closed && len(b.out) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b.out)
	b.out = b.out[n:]
	return n, nil
}

// Write feeds host bytes to the fake and processes whole frames.
func (b *BootSim) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return 0, io.ErrClosedPipe
	}
	b.in = append(b.in, p...)
	b.process()
	return len(p), nil
}

// Close stops the fake and unblocks Read.
func (b *BootSim) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		b.cond.Broadcast()
	}
	return nil
}

// SetBaudRate accepts a line-speed change; the fake does not model baud, so
// it is a no-op that keeps the transport usable with a baud setter.
func (b *BootSim) SetBaudRate(int) error { return nil }

// process consumes whole frames from b.in. b.mu is held.
func (b *BootSim) process() {
	for len(b.in) > 0 {
		switch b.in[0] {
		case autobaudByte:
			// Consume the run of 'U' bytes; answer the first time of a stream.
			i := 0
			for i < len(b.in) && b.in[i] == autobaudByte {
				i++
			}
			b.in = b.in[i:]
			// Every autobaud run starts a fresh stream: this models the
			// device rebooting into its loader, which is how a restart
			// recovers an interrupted update (spec Q5).
			b.startStream()
			b.queue([]byte{ackAlive})
		case sof:
			if !b.takeFrame() {
				return // wait for more bytes
			}
		default:
			b.in = b.in[1:] // resync: drop one byte
		}
	}
}

// startStream resets per-stream state for a new autobaud sync.
func (b *BootSim) startStream() {
	b.stream++
	b.acked = 0
	b.written = nil
}

// takeFrame tries to parse one start-of-frame frame from the front of b.in.
// It returns false if more bytes are needed. b.mu is held.
func (b *BootSim) takeFrame() bool {
	if len(b.in) < 2 {
		return false
	}
	n := int(b.in[1]&^lenHighBit) + 1 // payload+checksum length before escaping
	// Walk the escaped body until n unescaped bytes are collected.
	unesc := make([]byte, 0, n)
	i := 2
	for len(unesc) < n {
		if i >= len(b.in) {
			return false // incomplete; wait
		}
		c := b.in[i]
		unesc = append(unesc, c)
		i++
		if c == 0x00 {
			if i >= len(b.in) {
				return false
			}
			i++ // skip the stuffing byte
		}
	}
	b.in = b.in[i:]
	b.handleFrame(unesc)
	return true
}

// handleFrame validates a decoded payload+checksum and acknowledges it. A
// record frame (index + address + data block) has its block index recorded;
// a shorter frame is a command. b.mu is held.
func (b *BootSim) handleFrame(buf []byte) {
	var s byte
	for _, c := range buf {
		s += c
	}
	if s != 0 { // payload+checksum must sum to 0
		b.queue(buildAck([]byte{ackFail}))
		return
	}
	payload := buf[:len(buf)-1]
	isRecord := len(payload) == 2+4+dataBlockLen
	if isRecord {
		b.acked++
		if b.interrupting() {
			return // drop the ack: the stream stalls
		}
		b.written = append(b.written, binary.LittleEndian.Uint16(payload[:2]))
	}
	b.queue(buildAck([]byte{ackAlive}))
}

// interrupting reports whether this record's ack should be dropped.
func (b *BootSim) interrupting() bool {
	if b.cfg.InterruptAfter <= 0 {
		return false
	}
	if b.cfg.InterruptOnce && b.stream > 1 {
		return false
	}
	return b.acked > b.cfg.InterruptAfter
}

// queue appends bytes for the host to read. b.mu is held.
func (b *BootSim) queue(p []byte) {
	b.out = append(b.out, p...)
	b.cond.Broadcast()
}

// static check: BootSim is a usable reprogram transport.
var _ interface {
	io.ReadWriteCloser
	SetBaudRate(int) error
} = (*BootSim)(nil)
