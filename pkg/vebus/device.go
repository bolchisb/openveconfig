package vebus

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// NotApplicable is the raw setting value that marks a setting the device
// knows but that does not apply to its current configuration.
// COMMUNITY: xcellsior/ve-bus-programming FINDINGS.md §9 ("Unsupported" vs.
// "No Response"). It is not taken as such for flag words, or for a setting
// whose device-reported Maximum is 0xFFFF, where it is an ordinary value.
const NotApplicable = 0xFFFF

var (
	// ErrUnsupported is returned for a variable the device does not report
	// as supported.
	ErrUnsupported = errors.New("vebus: not supported by the device")
	// ErrNotApplicable is returned by Display and Absolute for a setting
	// whose value is NotApplicable.
	ErrNotApplicable = errors.New("vebus: value not applicable")
)

// Access is the access byte a device reports for a setting in Winmon mode 2
// (§7.3.8 "Access level"). The meaning of its bits is reverse-engineered
// (#6): bit 0 = a password is required to change the setting (OBSERVED as
// the gate the original tool applies), bit 7 = the device must be reset for
// a change to take effect (INFERRED). Other bits are unknown.
type Access byte

// PasswordRequired reports bit 0.
func (a Access) PasswordRequired() bool { return a&0x01 != 0 }

// ResetRequired reports bit 7.
func (a Access) ResetRequired() bool { return a&0x80 != 0 }

type settingVar struct {
	info        mk2.SettingInfo
	value, read uint16
	hasRead     bool
}

type ramVar struct {
	info        mk2.RAMInfo
	value, read uint16
	hasRead     bool
}

// Device is the settings model of one VE.Bus device. It keeps the options
// the device reports for each variable (scale, offset, default, minimum,
// maximum and access byte, as mk2.SettingInfo and mk2.RAMInfo) apart from
// the values: the current, possibly edited value and the value last read
// from the device.
//
// A variable exists in the model only if the device reports it as
// supported: a setting or RAM variable when its Sc is not 0 (§7.3.8,
// §7.3.9), a flag when its bit is set in its word's Maximum (§7.3.13.3).
//
// A Device is not safe for concurrent use.
type Device struct {
	Address  byte
	Firmware uint32

	cat      *Catalogue
	settings map[uint16]*settingVar
	ram      map[uint16]*ramVar
}

// NewDevice returns an empty model that uses cat, or DefaultCatalogue if
// cat is nil.
func NewDevice(cat *Catalogue) *Device {
	if cat == nil {
		cat = DefaultCatalogue()
	}
	return &Device{cat: cat, settings: map[uint16]*settingVar{}, ram: map[uint16]*ramVar{}}
}

// Catalogue returns the catalogue the model uses.
func (d *Device) Catalogue() *Catalogue { return d.cat }

// AddSetting records a setting as the device reported it: its info and the
// raw value just read, which becomes both the current and the last-read
// value. A setting with Sc 0 is unsupported and is not recorded.
func (d *Device) AddSetting(id uint16, info mk2.SettingInfo, raw uint16) {
	if info.Sc == 0 {
		delete(d.settings, id)
		return
	}
	d.settings[id] = &settingVar{info: info, value: raw, read: raw, hasRead: true}
}

// AddRAM records a RAM variable as the device reported it, like AddSetting.
func (d *Device) AddRAM(id uint16, info mk2.RAMInfo, raw uint16) {
	if info.Sc == 0 {
		delete(d.ram, id)
		return
	}
	d.ram[id] = &ramVar{info: info, value: raw, read: raw, hasRead: true}
}

// SetRead records a value just read from (or confirmed on) the device for a
// setting or RAM variable: it becomes the current and last-read value. Use
// the flag's word to record a flag.
func (d *Device) SetRead(id ID, raw uint16) error {
	switch id.Kind {
	case Setting:
		s, ok := d.settings[id.Num]
		if !ok {
			return d.unsupported(id)
		}
		s.value, s.read, s.hasRead = raw, raw, true
	case RAM:
		r, ok := d.ram[id.Num]
		if !ok {
			return d.unsupported(id)
		}
		r.value, r.read, r.hasRead = raw, raw, true
	default:
		return fmt.Errorf("vebus: SetRead: %s: record the flag word instead", id)
	}
	return nil
}

// SettingInfo returns the options the device reported for setting id.
func (d *Device) SettingInfo(id uint16) (mk2.SettingInfo, bool) {
	s, ok := d.settings[id]
	if !ok {
		return mk2.SettingInfo{}, false
	}
	return s.info, true
}

// RAMInfo returns the options the device reported for RAM variable id.
func (d *Device) RAMInfo(id uint16) (mk2.RAMInfo, bool) {
	r, ok := d.ram[id]
	if !ok {
		return mk2.RAMInfo{}, false
	}
	return r.info, true
}

// Supported reports whether the device supports id.
func (d *Device) Supported(id ID) bool {
	switch id.Kind {
	case Setting:
		_, ok := d.settings[id.Num]
		return ok
	case RAM:
		_, ok := d.ram[id.Num]
		return ok
	case Flag:
		_, _, ok := d.flagWord(id.Num)
		return ok
	}
	return false
}

// flagWord returns the word holding flag n and its bit, if the flag is
// supported.
func (d *Device) flagWord(n uint16) (*settingVar, uint, bool) {
	w, bit, ok := d.cat.FlagLocation(n)
	if !ok {
		return nil, 0, false
	}
	s, ok := d.settings[w]
	if !ok || s.info.Max>>bit&1 == 0 {
		return nil, 0, false
	}
	return s, bit, true
}

// Entries returns the catalogue entries the device supports, in catalogue
// order. This is what a user interface shows.
func (d *Device) Entries() []Entry {
	var out []Entry
	for _, e := range d.cat.entries {
		if d.Supported(e.ID()) {
			out = append(out, e)
		}
	}
	return out
}

// SettingIDs returns the IDs of all supported settings, including those not
// in the catalogue, in ascending order.
func (d *Device) SettingIDs() []uint16 { return sortedKeys(d.settings) }

// RAMIDs returns the IDs of all supported RAM variables in ascending order.
func (d *Device) RAMIDs() []uint16 { return sortedKeys(d.ram) }

func sortedKeys[V any](m map[uint16]V) []uint16 {
	ids := make([]uint16, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Name returns the catalogue name of id, or a generic one.
func (d *Device) Name(id ID) string {
	if e, ok := d.cat.Entry(id); ok {
		return e.Name
	}
	return id.String()
}

// label is the name and ID, for messages.
func (d *Device) label(id ID) string {
	if e, ok := d.cat.Entry(id); ok {
		return fmt.Sprintf("%s (%s)", e.Name, id)
	}
	return id.String()
}

func (d *Device) unit(id ID) string {
	if e, ok := d.cat.Entry(id); ok && e.Unit != "" {
		return " " + e.Unit
	}
	return ""
}

func (d *Device) unsupported(id ID) error {
	return fmt.Errorf("%s: %w", d.label(id), ErrUnsupported)
}

// Raw returns the current raw value: the stored 16-bit value of a setting
// or RAM variable, 0 or 1 for a flag.
func (d *Device) Raw(id ID) (uint16, error) {
	switch id.Kind {
	case Setting:
		if s, ok := d.settings[id.Num]; ok {
			return s.value, nil
		}
	case RAM:
		if r, ok := d.ram[id.Num]; ok {
			return r.value, nil
		}
	case Flag:
		if s, bit, ok := d.flagWord(id.Num); ok {
			return s.value >> bit & 1, nil
		}
	}
	return 0, d.unsupported(id)
}

// NotApplicable reports whether setting id holds NotApplicable.
func (d *Device) NotApplicable(id ID) bool {
	if id.Kind != Setting {
		return false
	}
	s, ok := d.settings[id.Num]
	return ok && d.isNA(id.Num, s, s.value)
}

func (d *Device) isNA(id uint16, s *settingVar, raw uint16) bool {
	return raw == NotApplicable && s.info.Max != NotApplicable && !d.cat.IsFlagWord(id)
}

// Display returns the current display value: Scale * (raw + Offset) for a
// setting (§7.3.8) or RAM variable (§7.3.9, single-bit variables give 0 or
// 1), and 0 or 1 for a flag. It returns ErrNotApplicable for a setting that
// holds NotApplicable.
func (d *Device) Display(id ID) (float64, error) {
	switch id.Kind {
	case Setting:
		s, ok := d.settings[id.Num]
		if !ok {
			break
		}
		if d.isNA(id.Num, s, s.value) {
			return 0, fmt.Errorf("%s: %w", d.label(id), ErrNotApplicable)
		}
		return s.info.Value(s.value), nil
	case RAM:
		if r, ok := d.ram[id.Num]; ok {
			return r.info.Value(r.value), nil
		}
	case Flag:
		raw, err := d.Raw(id)
		return float64(raw), err
	}
	return 0, d.unsupported(id)
}

// Default returns the device-reported default as a raw value. For a flag it
// is the flag's bit of its word's Default (reverse-engineered (#6),
// OBSERVED).
func (d *Device) Default(id ID) (uint16, error) {
	switch id.Kind {
	case Setting:
		if s, ok := d.settings[id.Num]; ok {
			return s.info.Default, nil
		}
	case Flag:
		if s, bit, ok := d.flagWord(id.Num); ok {
			return s.info.Default >> bit & 1, nil
		}
	case RAM:
		if _, ok := d.ram[id.Num]; ok {
			return 0, fmt.Errorf("%s: RAM variables have no default", d.label(id))
		}
	}
	return 0, d.unsupported(id)
}

// Limits returns the allowed display range of a setting (the device's
// Minimum and Maximum, §7.3.8) or flag (0 to 1). Flag words have no range:
// their Maximum is the mask of supported flags (§7.3.13.3).
func (d *Device) Limits(id ID) (min, max float64, err error) {
	switch id.Kind {
	case Setting:
		s, ok := d.settings[id.Num]
		if !ok {
			break
		}
		if d.cat.IsFlagWord(id.Num) {
			return 0, 0, fmt.Errorf("%s: a flag word has no range", d.label(id))
		}
		return s.info.Value(s.info.Min), s.info.Value(s.info.Max), nil
	case Flag:
		if d.Supported(id) {
			return 0, 1, nil
		}
	case RAM:
		if _, ok := d.ram[id.Num]; ok {
			return 0, 0, fmt.Errorf("%s: RAM variables have no device limits", d.label(id))
		}
	}
	return 0, 0, d.unsupported(id)
}

// Step returns the smallest change of the display value: the scale of a
// setting or RAM variable, 1 for a flag or single-bit RAM variable.
func (d *Device) Step(id ID) (float64, error) {
	switch id.Kind {
	case Setting:
		if s, ok := d.settings[id.Num]; ok {
			return s.info.Scale(), nil
		}
	case RAM:
		if r, ok := d.ram[id.Num]; ok {
			if r.info.IsBit() {
				return 1, nil
			}
			return r.info.Scale(), nil
		}
	case Flag:
		if d.Supported(id) {
			return 1, nil
		}
	}
	return 0, d.unsupported(id)
}

// Access returns the access byte the device reported for a setting, or for
// a flag the byte of its word. ok is false when the device sent none (Winmon
// mode 1) or id is a RAM variable.
func (d *Device) Access(id ID) (a Access, ok bool) {
	var s *settingVar
	switch id.Kind {
	case Setting:
		s = d.settings[id.Num]
	case Flag:
		s, _, _ = d.flagWord(id.Num)
	}
	if s == nil || !s.info.HasAccessLevel {
		return 0, false
	}
	return Access(s.info.AccessLevel), true
}

// ResetRequired reports whether a change of id takes effect only after a
// device reset: the catalogue says so (§7.3.13.2), the firmware revision
// needs it (resetByRevision), or the device's access byte has bit 7 set.
func (d *Device) ResetRequired(id ID) bool {
	if e, ok := d.cat.Entry(id); ok && e.ResetRequired {
		return true
	}
	rev := d.Firmware % 1000
	for _, r := range resetByRevision {
		if rev < r.minRev || rev > r.maxRev {
			continue
		}
		for _, x := range r.ids {
			if x == id {
				return true
			}
		}
	}
	a, ok := d.Access(id)
	return ok && a.ResetRequired()
}

// Flag returns the current state of flag n.
func (d *Device) Flag(n uint16) (bool, error) {
	raw, err := d.Raw(FlagID(n))
	return raw == 1, err
}

// SetFlag sets flag n in its word. A flag the device does not support is
// refused: setting unsupported bits can change other flags (§7.3.13.3).
// A flag that must hold the inverse of another (catalogue InverseOf, such
// as flags 3 and 7, §7.3.13.3 "Keep flags[7] consistent") is kept in step:
// setting one sets the other to the inverse value, if it is supported.
func (d *Device) SetFlag(n uint16, v bool) error {
	if err := d.setBit(n, v); err != nil {
		return err
	}
	for _, e := range d.cat.entries {
		if e.Kind != Flag || e.InverseOf == nil {
			continue
		}
		var partner uint16
		switch {
		case e.Num == n:
			partner = *e.InverseOf
		case *e.InverseOf == n:
			partner = e.Num
		default:
			continue
		}
		if d.Supported(FlagID(partner)) {
			_ = d.setBit(partner, !v)
		}
	}
	return nil
}

func (d *Device) setBit(n uint16, v bool) error {
	s, bit, ok := d.flagWord(n)
	if !ok {
		return d.unsupported(FlagID(n))
	}
	if v {
		s.value |= 1 << bit
	} else {
		s.value &^= 1 << bit
	}
	return nil
}

// SetRaw sets a raw value after checking it (see ValidateRaw). A flag takes
// 0 or 1.
func (d *Device) SetRaw(id ID, raw uint16) error {
	if err := d.ValidateRaw(id, raw); err != nil {
		return err
	}
	switch id.Kind {
	case Setting:
		d.settings[id.Num].value = raw
	case RAM:
		d.ram[id.Num].value = raw
	case Flag:
		return d.SetFlag(id.Num, raw == 1)
	}
	return nil
}

// SetDisplay converts a display value to raw and sets it, after checking
// it (see ValidateDisplay). For a single-bit RAM variable only its bit of
// the stored word changes.
func (d *Device) SetDisplay(id ID, v float64) error {
	raw, err := d.displayToRaw(id, v)
	if err != nil {
		return err
	}
	return d.SetRaw(id, raw)
}

// Absolute returns the level a setting stands for. For a setting stored
// relative to another (catalogue RelativeTo, for example the DC low restart
// level 11 + 12, §7.3.13.2) it adds up the chain; otherwise it equals
// Display.
func (d *Device) Absolute(id uint16) (float64, error) {
	v, err := d.Display(SettingID(id))
	if err != nil {
		return 0, err
	}
	e, ok := d.cat.Entry(SettingID(id))
	if !ok || e.RelativeTo == nil {
		return v, nil
	}
	base, err := d.Absolute(e.RelativeTo.Base)
	if err != nil {
		return 0, err
	}
	return base + float64(e.RelativeTo.Sign)*v, nil
}

// SetAbsolute sets a setting so that Absolute(id) becomes v, given the
// current values of the settings it is relative to.
func (d *Device) SetAbsolute(id uint16, v float64) error {
	e, ok := d.cat.Entry(SettingID(id))
	if !ok || e.RelativeTo == nil {
		return d.SetDisplay(SettingID(id), v)
	}
	base, err := d.Absolute(e.RelativeTo.Base)
	if err != nil {
		return err
	}
	return d.SetDisplay(SettingID(id), float64(e.RelativeTo.Sign)*(v-base))
}

// Dirty reports whether the current value of id differs from the value
// last read from the device. A variable never read is dirty.
func (d *Device) Dirty(id ID) bool {
	switch id.Kind {
	case Setting:
		s, ok := d.settings[id.Num]
		return ok && (!s.hasRead || s.value != s.read)
	case RAM:
		r, ok := d.ram[id.Num]
		return ok && (!r.hasRead || r.value != r.read)
	case Flag:
		s, bit, ok := d.flagWord(id.Num)
		return ok && (!s.hasRead || (s.value^s.read)>>bit&1 != 0)
	}
	return false
}

// ChangedIDs returns the dirty settings (flag words included) and RAM
// variables: what a write to the device has to send.
func (d *Device) ChangedIDs() []ID {
	var out []ID
	for _, id := range d.SettingIDs() {
		if d.Dirty(SettingID(id)) {
			out = append(out, SettingID(id))
		}
	}
	for _, id := range d.RAMIDs() {
		if d.Dirty(RAMID(id)) {
			out = append(out, RAMID(id))
		}
	}
	return out
}

// Revert sets every value back to the value last read.
func (d *Device) Revert() {
	for _, s := range d.settings {
		if s.hasRead {
			s.value = s.read
		}
	}
	for _, r := range d.ram {
		if r.hasRead {
			r.value = r.read
		}
	}
}

// MergeFlagWord applies the flags changed in the model to current, the
// word as just read from the device, and returns the word to write: a
// read-modify-write that leaves bits the user did not change as the device
// has them. Flag registers need read-modify-write (§7.3.13.3 warnings;
// COMMUNITY: FINDINGS.md §8). It fails if a changed bit is not supported,
// which SetFlag and SetRaw already prevent.
func (d *Device) MergeFlagWord(word uint16, current uint16) (uint16, error) {
	id := SettingID(word)
	s, ok := d.settings[word]
	if !ok || !d.cat.IsFlagWord(word) {
		return 0, fmt.Errorf("%s: not a supported flag word", d.label(id))
	}
	mask := s.info.Max
	if s.hasRead {
		mask = s.value ^ s.read
	}
	if bad := mask &^ s.info.Max; bad != 0 {
		return 0, fmt.Errorf("%s: changed bits 0x%04X are not supported by the device", d.label(id), bad)
	}
	return current&^mask | s.value&mask, nil
}

// displayToRaw converts and checks a display value.
func (d *Device) displayToRaw(id ID, v float64) (uint16, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, d.issue(id, Error, "range", srcDocScaling, "%s: %v is not a number", d.label(id), v)
	}
	switch id.Kind {
	case Flag:
		if !d.Supported(id) {
			return 0, d.unsupported(id)
		}
		if v != 0 && v != 1 {
			return 0, d.issue(id, Error, "range", srcDocFlags, "%s: %s is not allowed: a flag is 0 or 1", d.label(id), num(v))
		}
		return uint16(v), nil
	case Setting:
		s, ok := d.settings[id.Num]
		if !ok {
			return 0, d.unsupported(id)
		}
		if d.cat.IsFlagWord(id.Num) {
			if v != math.Trunc(v) || v < 0 || v > math.MaxUint16 {
				return 0, d.issue(id, Error, "range", srcDocFlags, "%s: %s is not a 16-bit flag word", d.label(id), num(v))
			}
			return uint16(v), nil
		}
		q := v/s.info.Scale() - float64(s.info.Offset)
		r := math.Round(q)
		if r < float64(s.info.Min) || r > float64(s.info.Max) {
			return 0, d.rangeIssue(id, s, num(v)+d.unit(id), Error)
		}
		if math.Abs(q-r) > stepTolerance {
			min, max := s.info.Value(s.info.Min), s.info.Value(s.info.Max)
			return 0, d.issue(id, Error, "step", srcDocScaling,
				"%s: %s%s is not a whole step; allowed are %s to %s%s in steps of %s%s",
				d.label(id), num(v), d.unit(id), num(min), num(max), d.unit(id), num(s.info.Scale()), d.unit(id))
		}
		return uint16(r), nil
	case RAM:
		rv, ok := d.ram[id.Num]
		if !ok {
			return 0, d.unsupported(id)
		}
		return d.ramRaw(id, rv, v)
	}
	return 0, d.unsupported(id)
}

// stepTolerance is how far, in raw units, a display value may be from a
// whole step and still count as on it (float rounding of e.g. 13.8/0.01).
const stepTolerance = 1e-6

// ramRaw is the inverse of mk2.RAMInfo.Value (§7.3.9).
func (d *Device) ramRaw(id ID, r *ramVar, v float64) (uint16, error) {
	if r.info.IsBit() {
		bit := r.info.Bit()
		if bit < 0 || bit > 15 {
			return 0, fmt.Errorf("%s: invalid bit number %d", d.label(id), bit)
		}
		if v != 0 && v != 1 {
			return 0, d.issue(id, Error, "range", srcDocRAMScaling, "%s: %s is not allowed: a bit variable is 0 or 1", d.label(id), num(v))
		}
		return r.value&^(1<<uint(bit)) | uint16(v)<<uint(bit), nil
	}
	scale := r.info.Scale()
	q := v/scale - float64(r.info.Offset)
	x := math.Round(q)
	lo, hi := 0.0, float64(math.MaxUint16)
	if r.info.Signed() {
		lo, hi = math.MinInt16, math.MaxInt16
	}
	if x < lo || x > hi {
		return 0, d.issue(id, Error, "range", srcDocRAMScaling, "%s: %s%s is outside the allowed range %s to %s%s",
			d.label(id), num(v), d.unit(id), num(scale*(lo+float64(r.info.Offset))), num(scale*(hi+float64(r.info.Offset))), d.unit(id))
	}
	if math.Abs(q-x) > stepTolerance {
		return 0, d.issue(id, Error, "step", srcDocRAMScaling, "%s: %s%s is not a whole step of %s%s",
			d.label(id), num(v), d.unit(id), num(scale), d.unit(id))
	}
	if x < 0 {
		return uint16(int16(x)), nil
	}
	return uint16(x), nil
}
