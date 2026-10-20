package main

// VE.Bus error codes as the LEDs of a unit show them. The way the code is
// read from the LEDs and the meaning of each code are public: Victron,
// "VE.Bus Error Codes" and the "Error indications" chapter of the
// MultiPlus-II manual (ledSources). The texts are our own summaries of
// those pages.

// ledSources are the public pages the LED code information is based on.
var ledSources = []string{
	"https://www.victronenergy.com/live/ve.bus:ve.bus_error_codes",
	"https://www.victronenergy.com/media/pg/MultiPlus-II_230V/en/error-indications.html",
}

// ledState is the state of one LED.
type ledState int

const (
	ledOff ledState = iota
	ledFlashing
	ledOn
)

// ledCode returns the VE.Bus error code that the bulk, absorption and
// float LEDs show (the manual's three tables: the bulk LED selects the
// table, absorption the column and float the row). 0 means no code: at
// least one of the three must be lit or flashing.
func ledCode(bulk, absorption, float ledState) int {
	return 9*int(bulk) + 3*int(absorption) + int(float)
}

// errorCodeText says in our words what VE.Bus error code n means and what
// to do.
func errorCodeText(n int) string {
	switch n {
	case 0:
		return tr("No VE.Bus error code: at least one of the bulk, absorption and float LEDs must be lit or flashing.")
	case 1:
		return tr("Another phase of the system has switched off, for example because of overload, a low battery or a high temperature. Check the LEDs of the units that do not show this code.")
	case 2:
		return tr("Units with old and new versions of the VE.Bus protocol are mixed in one system. Contact your dealer.")
	case 3:
		return tr("Not every unit of the system was found. This often follows error 1. Check that the units are configured as one system, the VE.Bus cables and the DC fuses.")
	case 4:
		return tr("The leading unit does not find the other units. This can show briefly during a restart; otherwise check the VE.Bus cables.")
	case 5:
		return tr("Overvoltage on the AC output. Check the AC output wiring of the other units.")
	case 6:
		return tr("An error in an assistant. Update the firmware and the assistants, then configure the system again.")
	case 7:
		return tr("An external device is connected, but no assistant for it is installed.")
	case 8:
		return tr("The ground relay test failed. Check the installation for a neutral connected to ground and for leakage to ground. If it keeps coming back on a recent unit, the unit may need to be replaced.")
	case 10:
		return tr("The time synchronization of the system failed. This is usually caused by error 1 or a blown DC fuse.")
	case 11:
		return tr("The relay test failed, often because of the wiring. Check the AC wiring and the earth connection, and update the firmware.")
	case 12:
		return tr("The settings of the unit's two processors do not match. Send all settings again.")
	case 14:
		return tr("The unit cannot send on the VE.Bus, usually because of a short circuit in a VE.Bus cable.")
	case 15:
		return tr("Products, firmware or commands on the VE.Bus are not compatible. Update the firmware.")
	case 16:
		return tr("A VE.Bus dongle is missing. Update the firmware; current firmware needs no dongle.")
	case 17:
		return tr("A unit has lost the leader of its phase. Check the RJ45 sockets, the VE.Bus cables and the DC fuses, and update the firmware.")
	case 18:
		return tr("Overvoltage on the AC output of a unit that is switched off. Check for swapped AC wires.")
	case 19:
		return tr("A unit has no AC input while its AC input is switched as a group. Connect the AC input of every unit, or switch the grouping off in the System Configurator.")
	case 20:
		return tr("The configuration does not match: extended VE.Bus options are not allowed with a grid code.")
	case 22:
		return tr("This unit cannot follow another unit in a system; the model is too old.")
	case 24:
		return tr("The back-feed relay carries current while it should be open. Check the load at switching time, the AC input voltage limits and the relays of the units.")
	case 25:
		return tr("The units are not compatible: every unit needs the same firmware, and the units in one phase the same compatibility letter. Configure the system again.")
	case 26:
		return tr("An internal error. Switch all equipment off and on again; contact your dealer if it stays.")
	}
	return tr("This code is not described in the public sources below.")
}
