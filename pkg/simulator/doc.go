// Package simulator is an in-process fake MK2/MK3 interface with VE.Bus
// devices behind it. It lets the mk2 protocol code, the settings layer, the
// CLI and the GUI run without hardware, as a test target or a demo mode.
//
// The behaviour follows only the public document "Interfacing with VE.Bus
// products - MK2 Protocol 3.14"; section numbers (§) in comments refer to
// it. Where the document says nothing, the simulator does the simplest
// thing and its doc comment says so. The one exception is the MK3
// high-speed command 'N' (Config.HighSpeed), which follows community
// reports.
//
// The other exception is the VE.Bus system configuration (SystemUnit): the
// raw system frames that read and write each unit's system record, which
// are not in the MK2 document. They follow the project's behavioural spec
// (#53), written from reverse engineering of the original tools; where the
// device side is unknown the simulator follows what those tools expect and
// says so (assumptions to check on hardware, #3). Buses of system units
// are built with SystemDevice or the ready-made SystemFactory,
// SystemParallel2, SystemThreePhase and SystemMixedFirmware; units are
// switched on and off with SetPowered and made to misbehave with
// SetBehaviour.
//
// All device data is synthetic. The profiles (Synthetic12V230,
// Synthetic48V120) use setting and RAM IDs and names from §7.3.13 and, for
// some RAM variables, the example scale values printed in Appendix 2. Every
// other value (firmware versions, defaults, limits, live readings) was made
// up for this package. Nothing is taken from Victron software or its
// built-in demo devices. The same holds for system units: their long IDs,
// version words and letters are made up, and their records are the worked
// examples of the project's spec, computed from its rules.
//
// The host side is a *Port, an io.ReadWriteCloser with SetBaudRate, so it
// can stand in for a serial port:
//
//	sim := simulator.New(simulator.Config{})
//	c := mk2.NewClient(sim.Port(), nil)
//	defer c.Close() // closes the Port and stops the simulator
//
// A virtual serial port (pty or com0com pair) for external programs is out
// of scope for now.
//
// Faults (lost requests, lost or corrupted replies, "not supported" and
// "access level required" replies) are injected with InjectFault. Extra MK2
// commands can be added with Handle.
package simulator
