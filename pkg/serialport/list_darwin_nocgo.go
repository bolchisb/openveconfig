//go:build darwin && !cgo

package serialport

import (
	"strings"

	"go.bug.st/serial"
)

// List returns the /dev/cu.* serial ports. USB enumeration on macOS needs
// cgo, so without it no USB details are reported and every port is Unknown.
func List() ([]PortInfo, error) {
	names, err := serial.GetPortsList()
	if err != nil {
		return nil, err
	}
	var ports []PortInfo
	for _, name := range names {
		if strings.HasPrefix(name, "/dev/cu.") {
			ports = append(ports, newPortInfo(name, false, "", "", ""))
		}
	}
	return ports, nil
}
