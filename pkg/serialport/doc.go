// Package serialport lists and opens the serial ports that an MK2/MK3
// interface can be attached to.
//
// List returns every serial port the OS reports, with the USB VID, PID and
// serial number where available, and marks the ports whose USB identity
// matches a known interface (see Classify). A match is only a hint: the FTDI
// FT-X ID used by the MK3-USB is FTDI's default ID and is shared by many
// unrelated adapters, and the USB identity of the MK2-USB is not publicly
// known. Callers that auto-detect an interface should therefore also probe
// unmarked ports with the MK2 protocol (a 'V' frame) before giving up.
//
// Open opens a port at 2400 baud, 8N1, with DTR asserted (the RS232 MK2.2
// takes its power from DTR). The baud rate can be changed later on the open
// port.
//
// The port is driven by go.bug.st/serial (BSD 3-Clause license).
//
// # Platform notes
//
// Linux: serial devices such as /dev/ttyUSB0 belong to the dialout group, so
// the user must be a member of it (sudo usermod -aG dialout $USER, then log
// out and back in). Open reports a permission error with that hint.
//
// macOS: use the callout node /dev/cu.* rather than the dial-in node
// /dev/tty.*. Apple's serial device guide says the callout device "should
// almost always be used", and the dial-in device is meant for answering
// incoming calls. In the BSD serial model that macOS inherits, dial-in nodes
// need the Data Carrier Detect signal to work correctly and callout nodes do
// not. List reports the cu.* node. USB details on macOS come from IOKit and
// need cgo; a build with CGO_ENABLED=0 still lists the /dev/cu.* ports, but
// without VID, PID or serial number, so no port is marked and only a
// protocol probe can find the interface.
//
// Windows: ports are named COM1, COM2, and so on. Names above COM9 need the
// \\.\ device prefix in the Win32 API; the library adds it, so pass plain
// names such as "COM12". No cgo is needed. An "access denied" error from
// Windows means the port is already open in another program.
package serialport
