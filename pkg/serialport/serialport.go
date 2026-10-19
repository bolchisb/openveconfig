package serialport

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	"go.bug.st/serial"
)

// DefaultBaudRate is the MK2 protocol line speed.
const DefaultBaudRate = 2400

// Kind is a guess at what is attached to a port, based on its USB identity.
type Kind int

const (
	// Unknown means the USB identity matches no known interface, or there is
	// none. The port may still be an MK2 interface; probe it to find out.
	Unknown Kind = iota
	// MK3USB is an FTDI FT-X device (0403:6015), the identity community
	// reports give for the MK3-USB.
	MK3USB
	// PL2303 is a Prolific PL2303 USB-RS232 converter (067B:2303), as used
	// with the RS232 MK2.2.
	PL2303
)

// String returns a short human-readable name for k.
func (k Kind) String() string {
	switch k {
	case MK3USB:
		return "MK3-USB (FTDI FT-X)"
	case PL2303:
		return "PL2303 USB-RS232"
	default:
		return "unknown"
	}
}

// PortInfo describes one serial port.
type PortInfo struct {
	// Name is the name to pass to Open, e.g. COM3, /dev/ttyUSB0 or
	// /dev/cu.usbserial-XXXX.
	Name string
	// IsUSB reports whether the OS identified the port as a USB device.
	// VID, PID and SerialNumber are only set when it is true.
	IsUSB        bool
	VID, PID     uint16
	SerialNumber string
	// Kind is Classify(VID, PID) for USB ports and Unknown otherwise.
	Kind Kind
}

// Classify maps a USB vendor and product ID to a likely interface kind.
func Classify(vid, pid uint16) Kind {
	switch {
	case vid == 0x0403 && pid == 0x6015:
		return MK3USB
	case vid == 0x067B && pid == 0x2303:
		return PL2303
	default:
		return Unknown
	}
}

// newPortInfo builds a PortInfo from the hex VID/PID strings the
// enumerator reports.
func newPortInfo(name string, isUSB bool, vid, pid, serialNumber string) PortInfo {
	p := PortInfo{Name: name, IsUSB: isUSB}
	if isUSB {
		p.VID = parseHex16(vid)
		p.PID = parseHex16(pid)
		p.SerialNumber = serialNumber
		p.Kind = Classify(p.VID, p.PID)
	}
	return p
}

// parseHex16 parses a 16-bit hex ID, returning 0 if s is not one.
func parseHex16(s string) uint16 {
	v, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0
	}
	return uint16(v)
}

// Option changes how Open configures a port.
type Option func(*options)

type options struct {
	baudRate int
}

// WithBaudRate opens the port at baud instead of DefaultBaudRate.
func WithBaudRate(baud int) Option {
	return func(o *options) { o.baudRate = baud }
}

// Port is an open serial port. Its methods that change the configuration
// must not be called concurrently.
type Port struct {
	p    serial.Port
	mode serial.Mode
}

// Open opens the named port at 8N1 with DTR and RTS asserted, at
// DefaultBaudRate unless an option says otherwise. Reads block until data
// arrives; see SetReadTimeout.
func Open(name string, opts ...Option) (*Port, error) {
	o := options{baudRate: DefaultBaudRate}
	for _, opt := range opts {
		opt(&o)
	}
	mode := serial.Mode{
		BaudRate:          o.baudRate,
		DataBits:          8,
		Parity:            serial.NoParity,
		StopBits:          serial.OneStopBit,
		InitialStatusBits: &serial.ModemOutputBits{DTR: true, RTS: true},
	}
	p, err := serial.Open(name, &mode)
	if err != nil {
		return nil, openError(name, err)
	}
	return &Port{p: p, mode: mode}, nil
}

// Read reads from the port. When a read timeout is set and expires before
// any data arrives, Read returns 0, nil.
func (p *Port) Read(b []byte) (int, error) { return p.p.Read(b) }

// Write writes to the port.
func (p *Port) Write(b []byte) (int, error) { return p.p.Write(b) }

// Close closes the port.
func (p *Port) Close() error { return p.p.Close() }

// SetBaudRate changes the line speed of the open port, keeping 8N1.
func (p *Port) SetBaudRate(baud int) error {
	mode := p.mode
	mode.BaudRate = baud
	if err := p.p.SetMode(&mode); err != nil {
		return fmt.Errorf("set baud rate %d: %w", baud, err)
	}
	p.mode = mode
	return nil
}

// SetDTR sets or clears the DTR line.
func (p *Port) SetDTR(on bool) error { return p.p.SetDTR(on) }

// SetReadTimeout makes Read return after d without data. A negative d
// disables the timeout.
func (p *Port) SetReadTimeout(d time.Duration) error {
	if d < 0 {
		d = serial.NoTimeout
	}
	return p.p.SetReadTimeout(d)
}

// openError adds the port name to err and, for a permission error, tells
// the user how to fix it.
func openError(name string, err error) error {
	if isPermission(err) {
		return fmt.Errorf("open %s: %w (on Linux, add your user to the dialout group: "+
			"sudo usermod -aG dialout $USER, then log out and back in)", name, err)
	}
	return fmt.Errorf("open %s: %w", name, err)
}

// isPermission reports whether err means the user may not open the port.
// go.bug.st/serial reports EACCES as a PortError with code PermissionDenied.
func isPermission(err error) bool {
	var coded interface{ Code() serial.PortErrorCode }
	if errors.As(err, &coded) && coded.Code() == serial.PermissionDenied {
		return true
	}
	return errors.Is(err, fs.ErrPermission)
}
