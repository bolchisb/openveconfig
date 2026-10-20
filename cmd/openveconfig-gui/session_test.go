package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.bug.st/serial"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// withSimulator makes demo mode use a Simulator set up by setup.
func withSimulator(t *testing.T, setup func(*simulator.Simulator)) {
	t.Helper()
	orig := newSimulator
	t.Cleanup(func() { newSimulator = orig })
	newSimulator = func() *simulator.Simulator {
		s := orig()
		setup(s)
		return s
	}
}

// connectDemo opens a demo session that the test closes at the end.
func connectDemo(t *testing.T) (*session, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := connect(ctx, "", true, nil)
	if err == nil {
		t.Cleanup(func() { s.close() })
	}
	return s, err
}

func TestConnectDemo(t *testing.T) {
	var steps []int
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := connect(ctx, "", true, func(done, total int) {
		if total != connectSteps {
			t.Errorf("progress total = %d, want %d", total, connectSteps)
		}
		steps = append(steps, done)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if !s.demo || s.portName != "" {
		t.Errorf("demo = %v, port = %q; want a demo session without port", s.demo, s.portName)
	}
	if s.baud != mk2.HighSpeedBaudRate || s.interfaceType() != "MK3" {
		t.Errorf("baud = %d, type %q; want the MK3 high speed", s.baud, s.interfaceType())
	}
	if s.iface.Version != simulator.DefaultVersion {
		t.Errorf("interface version = %d, want %d", s.iface.Version, simulator.DefaultVersion)
	}
	if !slices.Equal(s.devices, []byte{0, 1}) || s.target != 0 {
		t.Errorf("devices = %v, target %d; want [0 1], target 0", s.devices, s.target)
	}
	for a, want := range map[byte]uint32{0: simulator.Synthetic12V230().Version, 1: simulator.Synthetic48V120().Version} {
		if s.firmware[a] != want {
			t.Errorf("firmware of %d = %d, want %d", a, s.firmware[a], want)
		}
	}
	if !slices.Equal(steps, []int{1, 2, 3, 4}) {
		t.Errorf("progress steps = %v, want [1 2 3 4]", steps)
	}
	status := describe(s)
	for _, want := range []string{"demo simulator", "MK3", "2 devices found", "address 0, firmware 2699481", "address 1, firmware 2799482"} {
		if !strings.Contains(status, want) {
			t.Errorf("status %q does not contain %q", status, want)
		}
	}

	// The session works, and fails once closed.
	ctx = context.Background()
	err = s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		_, err := c.SettingInfo(ctx, gateSetting)
		return err
	})
	if err != nil {
		t.Errorf("Do after connect: %v", err)
	}
	s.close()
	err = s.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		_, err := c.Version(ctx)
		return err
	})
	if !errors.Is(err, mk2.ErrClosed) {
		t.Errorf("Do after close = %v, want mk2.ErrClosed", err)
	}
}

func TestFirmwareGate(t *testing.T) {
	gate := func(count int) func(*simulator.Simulator) {
		return func(s *simulator.Simulator) {
			s.InjectFault(simulator.FaultRule{Cmd: mk2.CmdWinmon, WCmd: mk2.WCmdGetSettingInfo, Fault: simulator.Drop, Count: count})
		}
	}

	t.Run("no answer", func(t *testing.T) {
		withSimulator(t, gate(0))
		_, err := connectDemo(t)
		if !errors.Is(err, errFirmwareUnsupported) {
			t.Fatalf("connect = %v, want errFirmwareUnsupported", err)
		}
		for _, want := range []string{"2699481", "does not support automatic reading of setting limits"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q does not contain %q", err, want)
			}
		}
	})
	t.Run("answer on retry", func(t *testing.T) {
		withSimulator(t, gate(1))
		if _, err := connectDemo(t); err != nil {
			t.Fatalf("connect = %v, want success after one retry", err)
		}
	})
	t.Run("only one retry", func(t *testing.T) {
		withSimulator(t, gate(2))
		if _, err := connectDemo(t); !errors.Is(err, errFirmwareUnsupported) {
			t.Fatalf("connect = %v, want errFirmwareUnsupported", err)
		}
	})
}

func TestConnectCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connect(ctx, "", true, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("connect with a cancelled context = %v, want context.Canceled", err)
	}
}

func TestDoSerializes(t *testing.T) {
	s := &session{lock: make(chan struct{}, 1)}
	var inside, most atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			err := s.Do(context.Background(), func(context.Context, *mk2.Client) error {
				n := inside.Add(1)
				for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
				}
				time.Sleep(2 * time.Millisecond)
				inside.Add(-1)
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Errorf("%d ops ran at once, want 1", most.Load())
	}
}

func TestDoWaitHonoursContext(t *testing.T) {
	s := &session{lock: make(chan struct{}, 1)}
	release := make(chan struct{})
	started := make(chan struct{})
	go s.Do(context.Background(), func(context.Context, *mk2.Client) error {
		close(started)
		<-release
		return nil
	})
	<-started
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	ran := false
	err := s.Do(ctx, func(context.Context, *mk2.Client) error { ran = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || ran {
		t.Errorf("Do while busy = %v (op ran: %v), want context.DeadlineExceeded without running", err, ran)
	}
}

// codedErr stands in for *serial.PortError, whose code field cannot be set
// from outside the library.
type codedErr serial.PortErrorCode

func (e codedErr) Error() string              { return "serial error" }
func (e codedErr) Code() serial.PortErrorCode { return serial.PortErrorCode(e) }

func TestOpenFailure(t *testing.T) {
	tests := []struct {
		code serial.PortErrorCode
		want string
	}{
		{serial.PermissionDenied, "dialout"},
		{serial.PortBusy, "in use by another program"},
		{serial.PortNotFound, "was not found"},
		{serial.InvalidSerialPort, "Cannot open"},
	}
	for _, tt := range tests {
		cause := codedErr(tt.code)
		err := openFailure("/dev/ttyUSB0", cause)
		if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "/dev/ttyUSB0") {
			t.Errorf("openFailure(code %d) = %q, want the port and %q", tt.code, err, tt.want)
		}
		if !errors.Is(err, cause) {
			t.Errorf("openFailure(code %d) does not wrap its cause", tt.code)
		}
	}
}
