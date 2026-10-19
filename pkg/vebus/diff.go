package vebus

import "fmt"

// Difference is one setting or flag whose current value differs between two
// models, or that only one of them supports.
type Difference struct {
	ID   ID
	Name string
	// InA and InB report whether each model supports ID; A and B are the
	// value texts (ValueText), "" on a side that does not.
	InA, InB bool
	A, B     string
}

// DiffDevices lists the settings and flags whose current values differ
// between a and b (a file and a device, or two files), in ascending setting
// order, with names from a's catalogue. A flag word is compared flag by
// flag for the flags the catalogue names; differing bits it does not name
// are reported as the word itself. RAM variables are live readings, not
// configuration, and are not compared.
func DiffDevices(a, b *Device) []Difference {
	ids := map[uint16]bool{}
	for _, id := range a.SettingIDs() {
		ids[id] = true
	}
	for _, id := range b.SettingIDs() {
		ids[id] = true
	}
	var out []Difference
	for _, id := range sortedKeys(ids) {
		sa, inA := a.settings[id]
		sb, inB := b.settings[id]
		sid := SettingID(id)
		if inA && inB && sa.value == sb.value {
			continue
		}
		if inA && inB && a.cat.IsFlagWord(id) {
			out = append(out, diffFlags(a, b, id, sa.value^sb.value)...)
			continue
		}
		out = append(out, diffOf(a, b, sid, inA, inB))
	}
	return out
}

// diffFlags reports the named flags of word that differ, and the word if
// differing bits remain.
func diffFlags(a, b *Device, word, changed uint16) []Difference {
	var out []Difference
	first, _ := a.cat.flagsIn(word)
	for bit := uint16(0); bit < 16; bit++ {
		if changed>>bit&1 == 0 {
			continue
		}
		id := FlagID(first + bit)
		if _, ok := a.cat.Entry(id); !ok {
			continue
		}
		inA, inB := a.Supported(id), b.Supported(id)
		if !inA && !inB {
			continue
		}
		out = append(out, diffOf(a, b, id, inA, inB))
		changed &^= 1 << bit
	}
	if changed != 0 {
		out = append(out, diffOf(a, b, SettingID(word), true, true))
	}
	return out
}

func diffOf(a, b *Device, id ID, inA, inB bool) Difference {
	x := Difference{ID: id, Name: a.Name(id), InA: inA, InB: inB}
	if inA {
		x.A = a.ValueText(id)
	}
	if inB {
		x.B = b.ValueText(id)
	}
	return x
}

// ValueText formats the current value of id for display: the display value
// with its unit, the name of an enumerated value with the raw value, "n/a"
// for NotApplicable, a flag word in hex, a flag as on or off.
func (d *Device) ValueText(id ID) string {
	raw, err := d.Raw(id)
	if err != nil {
		return "unsupported"
	}
	switch {
	case id.Kind == Flag:
		if raw == 1 {
			return "on"
		}
		return "off"
	case id.Kind == Setting && d.cat.IsFlagWord(id.Num):
		return fmt.Sprintf("0x%04X", raw)
	case d.NotApplicable(id):
		return "n/a"
	}
	if e, ok := d.cat.Entry(id); ok {
		if name, ok := e.Choices[raw]; ok {
			return fmt.Sprintf("%s (%d)", name, raw)
		}
	}
	v, err := d.Display(id)
	if err != nil {
		return err.Error()
	}
	return num(v) + d.unit(id)
}
