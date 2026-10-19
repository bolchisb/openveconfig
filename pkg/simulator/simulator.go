package simulator

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// DefaultVersion is the MK2 version reported by 'V' unless Config.Version
// is set. It is the example value shown in Appendix 2 (1130131).
const DefaultVersion = 1130131

// DefaultVersionInterval is how often an idle MK2 sends a 'V' frame on its
// own: "approximately once per second if nothing else is being sent" (§3.4).
const DefaultVersionInterval = time.Second

// DefaultBaudRate is the MK2 line speed (§3.1). Both the simulator and its
// Port start at this speed.
const DefaultBaudRate = 2400

// Config configures a Simulator. The zero value is one Synthetic12V230
// device at address 0 that sends idle 'V' frames once per second.
type Config struct {
	// Devices are the VE.Bus devices on the bus. nil means one
	// Synthetic12V230 device at address 0.
	Devices []Device
	// Version is the MK2 version reported by 'V' (§3.4). 0 means
	// DefaultVersion.
	Version uint32
	// VersionInterval is how long the MK2 must be idle before it sends a
	// 'V' frame on its own. 0 means DefaultVersionInterval; a negative value
	// disables idle 'V' frames (useful in tests). The 'V' frame sent at
	// power-up (§3.4) and after 'R' (§3.6) is always sent.
	VersionInterval time.Duration
	// AccessLevel is the access level the host has. The spec does not say
	// how a host obtains one; the simulator refuses CommandWriteViaID on a
	// setting whose Setting.AccessLevel is higher with "access level
	// required" (0x9B, §7.3.11).
	AccessLevel byte
	// HighSpeed makes the simulator an MK3 that accepts the baud-rate change
	// 'N' (mk2.CmdBaudRate). This command is not in the MK2 document; its
	// behaviour here follows the community reports cited at
	// mk2.Client.NegotiateBaud: on 'N' the simulator moves its line to
	// mk2.HighSpeedBaudRate. It sends no reply, as the reply format is
	// unknown and neither the host code nor ve-ctrl-tool needs one (a
	// simulator choice). Without HighSpeed, 'N' is an unknown command and
	// gets no reply, which is how an MK2 is expected (not documented) to
	// behave.
	HighSpeed bool
}

// Device is one VE.Bus device on the simulated bus.
type Device struct {
	// Address is the device address, 0 to mk2.MaxAddress (§7.1).
	Address byte
	Profile Profile
}

// Handler handles one MK2 command frame from the host. It runs with the
// Simulator locked, so it must not call Simulator or Port methods; it
// replies and changes the line speed through l.
type Handler func(l *Line, req mk2.Frame)

// Line is the simulator's end of the serial line, as seen by a Handler.
// It is only valid during the Handler call.
type Line struct{ s *Simulator }

// Send sends f to the host, with the LED status appended if the host asked
// for it with 'S' (§5 <Flags[1]>).
func (l *Line) Send(f mk2.Frame) { l.s.send(f) }

// BaudRate returns the simulator's line speed.
func (l *Line) BaudRate() int { return l.s.baud }

// SetBaudRate changes the simulator's line speed. Frames sent before the
// call went out at the old speed.
func (l *Line) SetBaudRate(baud int) { l.s.baud = baud }

// Simulator is a simulated MK2 interface and its VE.Bus devices. Its
// methods are safe for concurrent use.
type Simulator struct {
	port *Port

	mu     sync.Mutex
	cond   *sync.Cond // broadcast when out grows or the port closes
	in     []byte     // received bytes not yet forming a frame
	out    []byte     // bytes waiting for Port.Read
	closed bool

	baud, hostBaud int
	idle           *time.Timer // sends the idle 'V' frame; nil if disabled
	interval       time.Duration
	lastTx         time.Time

	version   uint32
	access    byte
	handlers  map[byte]Handler
	devices   []*device // sorted by address; devices[0] is the master
	faults    []FaultRule
	fault     Fault // fault applied to the request being handled
	addr      byte  // set with 'A' (§7.1)
	mode      mk2.WinmonMode
	appendLED bool
	sw        mk2.Switch
	limit     uint16 // actual input current limit, 0.1 A (§6.2)
}

// New returns a running Simulator; it has already sent its power-up 'V'
// frame (§3.4). It panics if two devices share an address or an address is
// above mk2.MaxAddress. Close its Port to stop it.
func New(cfg Config) *Simulator {
	s := &Simulator{
		baud:     DefaultBaudRate,
		hostBaud: DefaultBaudRate,
		version:  cfg.Version,
		access:   cfg.AccessLevel,
		interval: cfg.VersionInterval,
		addr:     mk2.AddressNone,
		sw:       mk2.SwitchOn,
	}
	s.cond = sync.NewCond(&s.mu)
	s.port = &Port{s: s}
	if s.version == 0 {
		s.version = DefaultVersion
	}
	if s.interval == 0 {
		s.interval = DefaultVersionInterval
	}
	devs := cfg.Devices
	if devs == nil {
		devs = []Device{{Address: 0, Profile: Synthetic12V230()}}
	}
	for _, d := range devs {
		if d.Address > mk2.MaxAddress {
			panic(fmt.Sprintf("simulator: device address 0x%02X above 0x%02X", d.Address, mk2.MaxAddress))
		}
		if s.device(d.Address) != nil {
			panic(fmt.Sprintf("simulator: two devices at address 0x%02X", d.Address))
		}
		s.devices = append(s.devices, newDevice(d))
	}
	slices.SortFunc(s.devices, func(a, b *device) int { return int(a.addr) - int(b.addr) })
	if m := s.master(); m != nil {
		s.limit = m.p.InputLimit
	}
	s.handlers = map[byte]Handler{
		mk2.CmdVersion: s.cmdVersion,
		mk2.CmdLED:     s.cmdLED,
		mk2.CmdState:   s.cmdState,
		mk2.CmdFrame:   s.cmdFrame,
		mk2.CmdAddress: s.cmdAddress,
		mk2.CmdReset:   s.cmdReset,
	}
	for _, tag := range []byte("WXYZxyz") {
		s.handlers[tag] = s.cmdWinmon
	}
	if cfg.HighSpeed {
		s.handlers[mk2.CmdBaudRate] = s.cmdBaudRate
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.interval > 0 {
		s.idle = time.AfterFunc(s.interval, s.idleVersion)
	}
	s.sendVersion()
	return s
}

// Port returns the host side of the serial line.
func (s *Simulator) Port() *Port { return s.port }

// Handle registers h for the MK2 command cmd, replacing any built-in
// handler. Use it to add commands the simulator does not implement.
func (s *Simulator) Handle(cmd byte, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[cmd] = h
}

// BaudRate returns the simulator's line speed.
func (s *Simulator) BaudRate() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.baud
}

// SetRAMVar sets the value of RAM variable id of the device at addr, for
// example to animate a demo. It reports whether the device and the
// variable exist.
func (s *Simulator) SetRAMVar(addr byte, id uint16, value uint16) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.device(addr)
	if d == nil {
		return false
	}
	if _, ok := d.ram[id]; !ok {
		return false
	}
	d.ram[id] = value
	return true
}

func (s *Simulator) device(addr byte) *device {
	for _, d := range s.devices {
		if d.addr == addr {
			return d
		}
	}
	return nil
}

// master is the device that answers system-wide requests ('L', 'F'): the
// one with the lowest address. It is nil on an empty bus.
func (s *Simulator) master() *device {
	if len(s.devices) == 0 {
		return nil
	}
	return s.devices[0]
}

// receive feeds bytes from the host to the MK2. s.mu is held.
func (s *Simulator) receive(b []byte) {
	if s.hostBaud != s.baud {
		return // a UART at the wrong speed reads only noise; drop it
	}
	s.in = append(s.in, b...)
	for {
		if s.hostBaud != s.baud {
			s.in = nil // a handler changed the speed; the rest is noise
			return
		}
		adv, tok, _ := mk2.ScanMK2Frames(s.in, false)
		if tok == nil {
			s.in = s.in[adv:]
			return
		}
		f, err := mk2.Decode(tok)
		s.in = s.in[adv:]
		if err == nil {
			s.dispatch(f)
		}
	}
}

// dispatch handles one request frame, applying any matching fault.
func (s *Simulator) dispatch(f mk2.Frame) {
	s.fault = s.takeFault(f)
	defer func() { s.fault = 0 }()
	if s.fault == Drop {
		return
	}
	if h := s.handlers[f.Command]; h != nil {
		h(&Line{s}, f)
	}
	// Unknown MK2 commands get no reply: the spec defines none.
}

// send encodes f and queues it for the host. s.mu is held.
func (s *Simulator) send(f mk2.Frame) {
	if s.fault == NoReply {
		return
	}
	if s.appendLED {
		led := s.led()
		f.LED = &led
	}
	b, err := f.Encode()
	if err != nil {
		panic("simulator: " + err.Error()) // a bug in a handler
	}
	if s.fault == Corrupt {
		b[len(b)-1] ^= 0xFF
	}
	s.lastTx = time.Now()
	if s.idle != nil {
		s.idle.Reset(s.interval)
	}
	if s.hostBaud != s.baud {
		return // sent at a speed the host does not read; lost
	}
	s.out = append(s.out, b...)
	s.cond.Broadcast()
}

func (s *Simulator) idleVersion() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The timer may have fired while a frame was being sent; send then
	// re-armed it.
	if s.closed || time.Since(s.lastTx) < s.interval {
		return
	}
	s.sendVersion()
}

// Port is the host side of the simulated serial line. Read blocks until
// the simulator sends something or the Port is closed. Bytes written are
// handled as they arrive; replies are ready to read when Write returns.
type Port struct{ s *Simulator }

// Read reads bytes sent by the simulated MK2. After Close it returns
// io.EOF.
func (p *Port) Read(b []byte) (int, error) {
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.out) == 0 && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		return 0, io.EOF
	}
	n := copy(b, s.out)
	s.out = s.out[n:]
	return n, nil
}

// Write sends bytes to the simulated MK2. If the Port's baud rate differs
// from the simulator's, the bytes are lost.
func (p *Port) Write(b []byte) (int, error) {
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	s.receive(b)
	return len(b), nil
}

// Close closes the Port and stops the simulator. It unblocks Read.
func (p *Port) Close() error {
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		if s.idle != nil {
			s.idle.Stop()
		}
		s.cond.Broadcast()
	}
	return nil
}

// SetBaudRate sets the Port's line speed. While it differs from the
// simulator's, bytes in both directions are lost.
func (p *Port) SetBaudRate(baud int) error {
	if baud <= 0 {
		return errors.New("simulator: invalid baud rate")
	}
	s := p.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hostBaud = baud
	return nil
}
