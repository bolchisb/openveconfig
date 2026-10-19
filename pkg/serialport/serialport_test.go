package serialport

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"go.bug.st/serial"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		vid, pid uint16
		want     Kind
	}{
		{0x0403, 0x6015, MK3USB},
		{0x067B, 0x2303, PL2303},
		{0x0403, 0x6001, Unknown}, // FT232R, not FT-X
		{0x067B, 0x23A3, Unknown},
		{0x6015, 0x0403, Unknown}, // swapped
		{0, 0, Unknown},
	}
	for _, tt := range tests {
		if got := Classify(tt.vid, tt.pid); got != tt.want {
			t.Errorf("Classify(%04X, %04X) = %v, want %v", tt.vid, tt.pid, got, tt.want)
		}
	}
}

func TestNewPortInfo(t *testing.T) {
	tests := []struct {
		name               string
		isUSB              bool
		vid, pid, serialNo string
		want               PortInfo
	}{
		{"/dev/ttyUSB0", true, "0403", "6015", "HQ1234ABCDE",
			PortInfo{Name: "/dev/ttyUSB0", IsUSB: true, VID: 0x0403, PID: 0x6015, SerialNumber: "HQ1234ABCDE", Kind: MK3USB}},
		{"COM4", true, "067b", "2303", "",
			PortInfo{Name: "COM4", IsUSB: true, VID: 0x067B, PID: 0x2303, Kind: PL2303}},
		{"COM1", false, "", "", "",
			PortInfo{Name: "COM1"}},
		// Non-USB ports never carry USB details, even if some are passed.
		{"/dev/ttyS0", false, "0403", "6015", "X",
			PortInfo{Name: "/dev/ttyS0"}},
		{"/dev/ttyACM0", true, "zz", "12345", "",
			PortInfo{Name: "/dev/ttyACM0", IsUSB: true}},
	}
	for _, tt := range tests {
		got := newPortInfo(tt.name, tt.isUSB, tt.vid, tt.pid, tt.serialNo)
		if got != tt.want {
			t.Errorf("newPortInfo(%q, %v, %q, %q, %q) = %+v, want %+v",
				tt.name, tt.isUSB, tt.vid, tt.pid, tt.serialNo, got, tt.want)
		}
	}
}

// codedErr stands in for *serial.PortError, whose code field cannot be set
// from outside the library.
type codedErr serial.PortErrorCode

func (e codedErr) Error() string              { return "serial error" }
func (e codedErr) Code() serial.PortErrorCode { return serial.PortErrorCode(e) }

func TestOpenErrorPermission(t *testing.T) {
	for _, cause := range []error{
		codedErr(serial.PermissionDenied),
		&fs.PathError{Op: "open", Path: "/dev/ttyUSB0", Err: fs.ErrPermission},
	} {
		err := openError("/dev/ttyUSB0", cause)
		if !strings.Contains(err.Error(), "dialout") {
			t.Errorf("openError(%v) = %q, want a dialout hint", cause, err)
		}
		if !strings.Contains(err.Error(), "/dev/ttyUSB0") {
			t.Errorf("openError(%v) = %q, want the port name", cause, err)
		}
		if !errors.Is(err, cause) {
			t.Errorf("openError(%v) does not wrap its cause", cause)
		}
	}
}

func TestOpenErrorOther(t *testing.T) {
	cause := codedErr(serial.PortBusy)
	err := openError("COM3", cause)
	if strings.Contains(err.Error(), "dialout") {
		t.Errorf("openError(busy) = %q, want no dialout hint", err)
	}
	if !errors.Is(err, cause) {
		t.Error("openError(busy) does not wrap its cause")
	}
}
