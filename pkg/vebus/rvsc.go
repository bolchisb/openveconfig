package vebus

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/rvsc"
)

// FromRVSC builds a model of one unit of a .rvsc/.rvms file for offline
// editing, using cat (DefaultCatalogue if nil). The options of each
// setting come from the file's setting info and the values from the unit;
// they are recorded as read, so Dirty and ChangedIDs report the edits made
// after loading. Settings whose info has Sc 0 are unsupported and left out,
// as from a device (§7.3.8). The file holds no RAM variables and no access
// bytes.
//
// Firmware is the unit's firmware number. Address is left 0: the file does
// not record the unit's bus address (Unit.Index is its index in the system,
// whose relation to the MK2 address is not known).
func FromRVSC(info *rvsc.SettingInfoTable, u *rvsc.Unit, cat *Catalogue) (*Device, error) {
	if len(info.Settings) != u.NumSettings() {
		return nil, fmt.Errorf("vebus: file has setting info for %d settings but unit %s has %d values",
			len(info.Settings), UnitLabel(u), u.NumSettings())
	}
	d := NewDevice(cat)
	d.Firmware = u.Firmware()
	for id, raw := range u.Settings() {
		d.AddSetting(uint16(id), info.Settings[id], raw)
	}
	return d, nil
}

// UnitLabel names a file unit for messages: its serial and long ID.
func UnitLabel(u *rvsc.Unit) string {
	return fmt.Sprintf("%q (long ID 0x%08X)", u.Serial, u.LongID)
}

// GridCodeSetting reports whether setting id belongs to the grid code:
// setting 81 and settings 128 to 191.
//
// COMMUNITY: kylehart/mk2vsc docs/SAFETY.md, "The grid code: what the writer
// never touches" (https://github.com/kylehart/mk2vsc): the grid code is
// setting 81, a block of settings 129-189 and three words the firmware keeps
// with it (128, 190 and 191). Victron protects the grid code (country
// standard, loss-of-mains behaviour, feed-in) with a password that installers
// get from their distributor (#9), and kylehart reports a system that went
// offline after a file changed only one of those words. This project accepts
// only credentials the user enters (docs/CLEAN-ROOM.md, "Access control"),
// and the grid code's encoding is not understood (#9), so these settings are
// not edited through files by default.
func GridCodeSetting(id uint16) bool { return id == 81 || id >= 128 && id <= 191 }

// SaveOptions controls SaveRVSC.
type SaveOptions struct {
	// Override skips the validation errors of CheckWrite. Only an explicit
	// user override should set it.
	Override bool
	// AllowGridCode permits changes to grid-code settings (GridCodeSetting).
	// Only an explicit user override should set it.
	AllowGridCode bool
}

// SaveRVSC stores the settings of d whose raw value differs from the
// unit's into u, through rvsc.Unit.SetSetting, and returns their IDs. Every
// other byte of the file stays as it is; marshal the rvsc.File afterwards.
//
// It refuses, changing nothing, when d's firmware is not the unit's (raw
// values are only meaningful with the same setting info), when d fails
// CheckWrite, or when a grid-code setting would change and
// opt.AllowGridCode is false.
func SaveRVSC(d *Device, u *rvsc.Unit, opt SaveOptions) ([]uint16, error) {
	if d.Firmware != u.Firmware() {
		return nil, fmt.Errorf("vebus: model firmware %d is not the firmware %d of unit %s",
			d.Firmware, u.Firmware(), UnitLabel(u))
	}
	if err := d.CheckWrite(opt.Override); err != nil {
		return nil, err
	}
	file := u.Settings()
	var changed, grid []uint16
	for _, id := range d.SettingIDs() {
		raw := d.settings[id].value
		if int(id) >= len(file) {
			return nil, fmt.Errorf("vebus: %s is not in the file, which has settings 0-%d", d.label(SettingID(id)), len(file)-1)
		}
		if raw == file[id] {
			continue
		}
		changed = append(changed, id)
		if GridCodeSetting(id) {
			grid = append(grid, id)
		}
	}
	if len(grid) > 0 && !opt.AllowGridCode {
		return nil, &GridCodeError{IDs: grid}
	}
	for _, id := range changed {
		if err := u.SetSetting(int(id), d.settings[id].value); err != nil {
			return nil, err
		}
	}
	return changed, nil
}

// GridCodeError is returned by SaveRVSC when a grid-code setting would
// change without SaveOptions.AllowGridCode.
type GridCodeError struct {
	IDs []uint16
}

func (e *GridCodeError) Error() string {
	ids := make([]string, len(e.IDs))
	for i, id := range e.IDs {
		ids[i] = fmt.Sprint(id)
	}
	return "vebus: refusing to change grid-code setting(s) " + strings.Join(ids, ", ") +
		": the grid code is protected by an installer password and its encoding is not known (see #9)"
}

// Match is the result of comparing a file unit with a device: what was
// compared, with the outcome, and what could not be compared and why.
type Match struct {
	Checks      []MatchCheck
	NotCompared []string
}

// MatchCheck is one comparison.
type MatchCheck struct {
	What   string
	OK     bool
	Detail string
}

// OK reports whether every comparison passed.
func (m *Match) OK() bool {
	for _, c := range m.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// ErrMismatch is wrapped by Match.Err.
var ErrMismatch = errors.New("vebus: file does not match the device")

// Err returns nil when the file matches, or an error wrapping ErrMismatch
// that lists the failed comparisons.
func (m *Match) Err() error {
	var bad []string
	for _, c := range m.Checks {
		if !c.OK {
			bad = append(bad, c.What+": "+c.Detail)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrMismatch, strings.Join(bad, "; "))
}

// String is a report for the user, one line per comparison.
func (m *Match) String() string {
	var b strings.Builder
	for _, c := range m.Checks {
		st := "ok      "
		if !c.OK {
			st = "MISMATCH"
		}
		fmt.Fprintf(&b, "%s %s: %s\n", st, c.What, c.Detail)
	}
	for _, s := range m.NotCompared {
		fmt.Fprintf(&b, "not compared: %s\n", s)
	}
	return b.String()
}

// MatchRVSC checks whether unit u of a file (with the file's setting info)
// fits device d, a model read from the bus (Load):
//
//   - Firmware: the unit's firmware number must equal the device's
//     (§7.3.1), all seven digits. Victron venus-platform behaviour
//     (reference, https://github.com/victronenergy/venus-platform,
//     src/vebus_backup.cpp): the GX restore offers a backup only for units
//     whose product ID and firmware version match; mk2vsc reports error 54
//     "incompatible version number" and 57 "product number mismatch"
//     (https://github.com/victronenergy/venus/wiki/dbus).
//   - Setting info: the file must have info for as many settings as the
//     unit has values, and every setting d holds must be in the file with
//     the same Sc, Offset, Default, Minimum and Maximum (§7.3.8); otherwise
//     the file's raw values mean something else on this device.
//
// The unit count is checked separately with CheckUnitCount. The serial
// number and long ID are not compared: pkg/mk2 has no command that reads
// them from a device, so the unit is chosen by the user. The result says
// so in NotCompared.
func MatchRVSC(info *rvsc.SettingInfoTable, u *rvsc.Unit, d *Device) *Match {
	m := &Match{}
	m.Checks = append(m.Checks, MatchCheck{
		What:   "firmware",
		OK:     u.Firmware() == d.Firmware,
		Detail: fmt.Sprintf("file unit %d, device %d", u.Firmware(), d.Firmware),
	})
	m.Checks = append(m.Checks, MatchCheck{
		What:   "settings count",
		OK:     len(info.Settings) == u.NumSettings(),
		Detail: fmt.Sprintf("file setting info %d, file unit %d values", len(info.Settings), u.NumSettings()),
	})
	var bad []string
	ids := d.SettingIDs()
	for _, id := range ids {
		dev := d.settings[id].info
		if int(id) >= len(info.Settings) {
			bad = append(bad, fmt.Sprintf("%d (not in file)", id))
			continue
		}
		if f := info.Settings[id]; !sameInfo(f, dev) {
			bad = append(bad, fmt.Sprintf("%d (file %s, device %s)", id, infoText(f), infoText(dev)))
		}
	}
	detail := fmt.Sprintf("%d settings the device reported have the same scale, offset, default and limits in the file", len(ids))
	if len(bad) > 0 {
		detail = fmt.Sprintf("%d of %d settings differ: %s", len(bad), len(ids), strings.Join(bad, ", "))
	}
	m.Checks = append(m.Checks, MatchCheck{What: "setting info", OK: len(bad) == 0, Detail: detail})
	m.NotCompared = append(m.NotCompared,
		fmt.Sprintf("serial number and long ID (file unit %s): no command in pkg/mk2 reads them from a device", UnitLabel(u)),
		"firmware subversion: Victron's GX restore also compares it, but it is not decoded from the file",
		fmt.Sprintf("settings the device model does not hold (%d in the file): only those read from the device are compared", u.NumSettings()-len(ids)),
		"assistant area: its encoding is unknown (#7)")
	return m
}

func sameInfo(a, b mk2.SettingInfo) bool {
	return a.Sc == b.Sc && a.Offset == b.Offset && a.Default == b.Default && a.Min == b.Min && a.Max == b.Max
}

func infoText(i mk2.SettingInfo) string {
	return fmt.Sprintf("Sc %d Offset %d Default %d Min %d Max %d", i.Sc, i.Offset, i.Default, i.Min, i.Max)
}

// CheckUnitCount compares the number of units in a file with the number of
// devices found on the bus. A file for a whole system must have one unit
// per device. With singleUnit, one unit of the file is applied to one
// device and the count is not required to match: Victron's GX restore
// offers that only as an explicit option ("allow single unit replace",
// Victron venus-platform behaviour (reference),
// https://github.com/victronenergy/venus-platform, src/vebus_backup.cpp).
func (m *Match) CheckUnitCount(fileUnits, busDevices int, singleUnit bool) {
	c := MatchCheck{What: "unit count", OK: fileUnits == busDevices,
		Detail: fmt.Sprintf("file %d, bus %d", fileUnits, busDevices)}
	if singleUnit {
		c.OK = true
		c.Detail += " (single unit: one file unit to one device)"
	}
	m.Checks = append(m.Checks, c)
}
