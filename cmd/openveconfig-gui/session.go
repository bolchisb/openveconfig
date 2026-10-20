package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"strconv"

	"go.bug.st/serial"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/serialport"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// gateSetting is the setting whose info is read right after connecting to
// check that the device firmware reports setting limits: setting 6, the AC
// input current limit (§7.3.13.2).
const gateSetting = 6

// session is one open connection to an interface, or to the demo simulator.
// The info fields are set by connect and are read-only afterwards, so any
// goroutine may read them. All device I/O goes through Do.
type session struct {
	portName string // serial port; empty in demo mode
	demo     bool
	baud     int         // line speed in use
	iface    mk2.Version // the interface's 'V' reply
	devices  []byte      // addresses that answered, in ascending order
	// firmware is the firmware version of each device; a device whose
	// version could not be read is missing.
	firmware map[byte]uint32
	// target is the device the settings apply to: the first device found.
	target byte

	line   transport
	client *mk2.Client
	lock   chan struct{} // holds one token while an op runs
}

// transport is the serial line under an mk2.Client: a serial port or the
// simulator's port.
type transport interface {
	io.ReadWriteCloser
	mk2.BaudRateSetter
}

// newSimulator returns the simulator used in demo mode. Tests replace it.
var newSimulator = func() *simulator.Simulator {
	return simulator.New(simulator.Config{HighSpeed: true, Devices: []simulator.Device{
		{Address: 0, Profile: simulator.Synthetic12V230()},
		{Address: 1, Profile: simulator.Synthetic48V120()},
	}})
}

// newSystemSimulator returns the simulator of the VE.Bus system demo: three
// new units, all with the factory record at address 0. Tests replace it.
var newSystemSimulator = func() *simulator.Simulator {
	return simulator.New(simulator.Config{HighSpeed: true, Devices: simulator.SystemFactory(3)})
}

// errFirmwareUnsupported means the target device did not answer the
// setting info request of the firmware gate.
var errFirmwareUnsupported = errors.New("device firmware does not report setting limits")

// uiError is an error with a message written for the user. It wraps the
// technical cause.
type uiError struct {
	msg string
	err error
}

func (e *uiError) Error() string { return e.msg }
func (e *uiError) Unwrap() error { return e.err }

// connectSteps is the total passed to the progress callback of connect.
const connectSteps = 4

// connect opens the serial port named port, or the demo simulator when demo
// is set, sets up the line speed, finds the devices, reads their firmware
// versions and applies the firmware gate. progress may be nil. The caller
// must close the session.
func connect(ctx context.Context, port string, demo bool, progress func(done, total int)) (*session, error) {
	if progress == nil {
		progress = func(int, int) {}
	}
	s := &session{portName: port, demo: demo, firmware: map[byte]uint32{}, lock: make(chan struct{}, 1)}
	if demo {
		s.portName = ""
		s.line = newSimulator().Port()
	} else {
		p, err := serialport.Open(port)
		if err != nil {
			return nil, openFailure(port, err)
		}
		s.line = p
	}
	// As in openveconfig-cli: settings are read back after a write, so a
	// missing write acknowledgement need not fail it.
	s.client = mk2.NewClient(s.line, &mk2.Options{IgnoreMissingWriteAck: true})
	if err := s.handshake(ctx, progress); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// connectSystemDemo is connect for the VE.Bus system demo
// (newSystemSimulator).
func connectSystemDemo(ctx context.Context, progress func(done, total int)) (*session, error) {
	if progress == nil {
		progress = func(int, int) {}
	}
	s := &session{demo: true, firmware: map[byte]uint32{}, lock: make(chan struct{}, 1), line: newSystemSimulator().Port()}
	s.client = mk2.NewClient(s.line, &mk2.Options{IgnoreMissingWriteAck: true})
	if err := s.handshake(ctx, progress); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

// handshake runs the connection steps of connect on the new client.
func (s *session) handshake(ctx context.Context, progress func(done, total int)) error {
	c := s.client
	baud, err := c.NegotiateBaud(ctx, s.line, mk2.HighSpeedAuto)
	if err != nil {
		return s.linkFailure(ctx, err)
	}
	s.baud = baud
	if s.iface, err = c.Version(ctx); err != nil {
		return s.linkFailure(ctx, err)
	}
	progress(1, connectSteps)

	if s.devices, err = c.Discover(ctx); err != nil {
		return s.deviceFailure(ctx, err)
	}
	if len(s.devices) == 0 {
		return &uiError{msg: tr("The interface answers, but no device answered on the bus. " +
			"Check that the inverter/charger is switched on and that the cable from the interface to it is plugged in.")}
	}
	s.target = s.devices[0]
	progress(2, connectSteps)

	for _, a := range s.devices {
		if err := c.SetAddress(ctx, a); err != nil {
			return s.deviceFailure(ctx, err)
		}
		v, err := c.SoftwareVersion(ctx)
		switch {
		case err == nil:
			s.firmware[a] = v
		case ctx.Err() != nil:
			return ctx.Err()
		}
	}
	progress(3, connectSteps)

	if err := s.checkFirmware(ctx); err != nil {
		return err
	}
	progress(4, connectSteps)
	return nil
}

// checkFirmware applies the firmware gate: the target device must answer
// the setting info request for gateSetting, which is tried twice.
func (s *session) checkFirmware(ctx context.Context) error {
	c := s.client
	if err := c.SetAddress(ctx, s.target); err != nil {
		return s.deviceFailure(ctx, err)
	}
	var err error
	for range 2 {
		if _, err = c.SettingInfo(ctx, gateSetting); !errors.Is(err, mk2.ErrTimeout) {
			break
		}
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, mk2.ErrTimeout):
		return &uiError{err: errFirmwareUnsupported, msg: tr(
			"The device at address {{.Address}} has firmware version {{.Firmware}}. "+
				"This firmware does not support automatic reading of setting limits and is not supported.",
			args{"Address": s.target, "Firmware": s.firmwareText(s.target)})}
	default:
		return s.deviceFailure(ctx, err)
	}
}

// firmwareText returns the firmware version of the device at addr for
// display.
func (s *session) firmwareText(addr byte) string {
	v, ok := s.firmware[addr]
	if !ok {
		return tr("unknown")
	}
	return strconv.FormatUint(uint64(v), 10)
}

// interfaceType names the kind of interface. Only an MK3 switches to the
// high line speed; the protocol has no other way to tell them apart.
func (s *session) interfaceType() string {
	if s.baud == mk2.HighSpeedBaudRate {
		return tr("MK3")
	}
	return tr("MK2 or MK3")
}

// Do runs op with the session's client. It serializes all device I/O: ops
// run one at a time, in no guaranteed order, and an op that sends a batch
// of requests keeps the client for the whole batch. Do waits for its turn
// until ctx ends. It must not be called on the UI goroutine, and op must
// not call Do.
func (s *session) Do(ctx context.Context, op func(ctx context.Context, c *mk2.Client) error) error {
	select {
	case s.lock <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.lock }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return op(ctx, s.client)
}

// close closes the client and the port, or stops the simulator. A running
// op fails with mk2.ErrClosed.
func (s *session) close() error { return s.client.Close() }

// openFailure turns an error from opening port into a message that says
// what to do.
func openFailure(port string, err error) error {
	var coded interface{ Code() serial.PortErrorCode }
	code := serial.PortErrorCode(-1)
	if errors.As(err, &coded) {
		code = coded.Code()
	}
	a := args{"Port": port, "Err": err}
	switch {
	case isPermission(err):
		return &uiError{err: err, msg: tr("You do not have permission to open {{.Port}}. "+
			"On Linux, add your user to the dialout group (sudo usermod -aG dialout $USER), then log out and back in.", a)}
	case code == serial.PortBusy:
		return &uiError{err: err, msg: tr("{{.Port}} is in use by another program. Close that program and try again.", a)}
	case code == serial.PortNotFound:
		return &uiError{err: err, msg: tr("{{.Port}} was not found. Check that the interface is plugged in, then refresh the port list.", a)}
	default:
		return &uiError{err: err, msg: tr("Cannot open {{.Port}}: {{.Err}}", a)}
	}
}

// isPermission reports whether err means the user may not open the port.
// go.bug.st/serial reports EACCES as a PortError with code PermissionDenied.
func isPermission(err error) bool {
	var coded interface{ Code() serial.PortErrorCode }
	return (errors.As(err, &coded) && coded.Code() == serial.PermissionDenied) || errors.Is(err, fs.ErrPermission)
}

// linkFailure describes an error while setting up the link to the
// interface.
func (s *session) linkFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, mk2.ErrNoLink) || errors.Is(err, mk2.ErrTimeout) {
		return &uiError{err: err, msg: tr("No interface answered on {{.Port}}. "+
			"Check that the interface is plugged in and that this is its port, or use auto-detect.", args{"Port": s.portName})}
	}
	return &uiError{err: err, msg: tr("The connection to the interface failed: {{.Err}}", args{"Err": err})}
}

// deviceFailure describes an error while talking to a device.
func (s *session) deviceFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, mk2.ErrTimeout) {
		return &uiError{err: err, msg: tr("The device is not answering. " +
			"Check that it is switched on and connected to the interface, then connect again.")}
	}
	return &uiError{err: err, msg: tr("Communication with the device failed: {{.Err}}", args{"Err": err})}
}
