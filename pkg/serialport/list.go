//go:build !darwin || cgo

package serialport

import "go.bug.st/serial/enumerator"

// List returns the serial ports present on the system, with USB details
// where the OS provides them.
func List() ([]PortInfo, error) {
	details, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	ports := make([]PortInfo, 0, len(details))
	for _, d := range details {
		ports = append(ports, newPortInfo(d.Name, d.IsUSB, d.VID, d.PID, d.SerialNumber))
	}
	return ports, nil
}
