package vebus

import (
	"fmt"
	"sort"
)

// Kind is the kind of a VE.Bus variable.
type Kind uint8

const (
	// Setting is a setting stored in the device's EEPROM, read with
	// CommandReadSetting (§7.3.4).
	Setting Kind = iota + 1
	// Flag is one bit of a flag-word setting (§7.3.13.3). Its number is the
	// flag number n of Flags[n].
	Flag
	// RAM is a RAM variable, read with CommandReadRAMVar (§7.3.3).
	RAM
)

func (k Kind) String() string {
	switch k {
	case Setting:
		return "setting"
	case Flag:
		return "flag"
	case RAM:
		return "RAM variable"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// ID identifies a variable: a setting ID, a flag number or a RAM ID.
type ID struct {
	Kind Kind
	Num  uint16
}

// SettingID returns the ID of setting n.
func SettingID(n uint16) ID { return ID{Setting, n} }

// FlagID returns the ID of flag n (Flags[n] in §7.3.13.3).
func FlagID(n uint16) ID { return ID{Flag, n} }

// RAMID returns the ID of RAM variable n.
func RAMID(n uint16) ID { return ID{RAM, n} }

func (id ID) String() string { return fmt.Sprintf("%s %d", id.Kind, id.Num) }

// Tab is the settings tab of the user interface an entry is edited on.
type Tab uint8

const (
	// TabNone marks entries that are not edited on a tab: flag words
	// (edited through their flags), system-configuration values, flags
	// that must not be changed by hand.
	TabNone Tab = iota
	TabGeneral
	TabGrid
	TabInverter
	TabCharger
	TabVirtualSwitch
	TabAdvanced
	// TabMonitor holds RAM variables (live readings).
	TabMonitor
)

func (t Tab) String() string {
	switch t {
	case TabNone:
		return "None"
	case TabGeneral:
		return "General"
	case TabGrid:
		return "Grid"
	case TabInverter:
		return "Inverter"
	case TabCharger:
		return "Charger"
	case TabVirtualSwitch:
		return "Virtual switch"
	case TabAdvanced:
		return "Advanced"
	case TabMonitor:
		return "Monitor"
	}
	return fmt.Sprintf("Tab(%d)", uint8(t))
}

// Relative describes a setting stored relative to another setting: its
// absolute level is Absolute(Base) + Sign*Display(this).
type Relative struct {
	Base uint16 // setting ID
	Sign int    // +1 or -1
}

// Entry describes one known variable.
type Entry struct {
	Kind Kind
	Num  uint16
	// Name comes from the source cited in Source.
	Name string
	// Unit is the unit of the display value (Scale * (raw + Offset)); ""
	// when unknown or dimensionless. For settings, §7.3.10 gives volt,
	// ampere and minute as the units of voltages, currents and times.
	Unit string
	// Tab is the UI tab the entry is edited on; see catalogue_data.go for
	// the basis.
	Tab Tab
	// Group is a sub-group label within the tab, of this project's own
	// wording.
	Group string
	// Source cites where Name and the facts in this entry come from.
	Source string

	// Settings only.
	// FlagWord marks a setting whose value is a word of flag bits; edit it
	// through its flags.
	FlagWord bool
	// Choices names some raw values of an enumerated setting.
	Choices map[uint16]string
	// RelativeTo, if set, says the setting is stored relative to another.
	RelativeTo *Relative
	// ResetRequired marks settings that become active only after a reset of
	// the device (§7.3.13.2, footnote e).
	ResetRequired bool

	// Flags only.
	// InverseOf, if set, is the flag whose inverse this flag must hold.
	InverseOf *uint16
	// NotPromoted marks a flag the document says must not be changed
	// ("Not promoted option", §7.3.13.3).
	NotPromoted bool
}

// ID returns the entry's ID.
func (e Entry) ID() ID { return ID{e.Kind, e.Num} }

// Catalogue is a set of known variables. It is read-only after creation.
type Catalogue struct {
	entries   []Entry
	byID      map[ID]int
	flagWords []uint16
}

// NewCatalogue builds a catalogue. flagWords lists the settings that hold
// flag bits, in order: flag n is bit n%16 of flagWords[n/16]. Every flag
// entry must fall in one of them, relations must point to entries of the
// right kind, and IDs must be unique.
func NewCatalogue(flagWords []uint16, entries []Entry) (*Catalogue, error) {
	c := &Catalogue{
		entries:   append([]Entry(nil), entries...),
		byID:      make(map[ID]int, len(entries)),
		flagWords: append([]uint16(nil), flagWords...),
	}
	sort.SliceStable(c.entries, func(i, j int) bool {
		a, b := c.entries[i], c.entries[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Num < b.Num
	})
	for i, e := range c.entries {
		if e.Kind < Setting || e.Kind > RAM {
			return nil, fmt.Errorf("vebus: catalogue entry %q: invalid kind %d", e.Name, e.Kind)
		}
		if e.Tab > TabMonitor {
			return nil, fmt.Errorf("vebus: catalogue entry %s: invalid tab %d", e.ID(), e.Tab)
		}
		if e.Name == "" || e.Source == "" {
			return nil, fmt.Errorf("vebus: catalogue entry %s: name and source are required", e.ID())
		}
		if _, dup := c.byID[e.ID()]; dup {
			return nil, fmt.Errorf("vebus: catalogue entry %s: duplicate", e.ID())
		}
		c.byID[e.ID()] = i
	}
	for _, e := range c.entries {
		switch {
		case e.Kind == Flag:
			if _, _, ok := c.FlagLocation(e.Num); !ok {
				return nil, fmt.Errorf("vebus: catalogue entry %s: no flag word holds it", e.ID())
			}
			if e.InverseOf != nil {
				if _, ok := c.byID[FlagID(*e.InverseOf)]; !ok {
					return nil, fmt.Errorf("vebus: catalogue entry %s: inverse of unknown flag %d", e.ID(), *e.InverseOf)
				}
			}
		case e.RelativeTo != nil:
			r := e.RelativeTo
			if _, ok := c.byID[SettingID(r.Base)]; !ok || e.Kind != Setting || (r.Sign != 1 && r.Sign != -1) {
				return nil, fmt.Errorf("vebus: catalogue entry %s: invalid relation", e.ID())
			}
		}
	}
	for _, w := range c.flagWords {
		if i, ok := c.byID[SettingID(w)]; !ok || !c.entries[i].FlagWord {
			return nil, fmt.Errorf("vebus: flag word %d: no setting entry with FlagWord set", w)
		}
	}
	// A relation chain must end; Absolute relies on it.
	for _, e := range c.entries {
		seen := map[uint16]bool{}
		for r := e.RelativeTo; r != nil; r = c.entries[c.byID[SettingID(r.Base)]].RelativeTo {
			if seen[r.Base] {
				return nil, fmt.Errorf("vebus: catalogue entry %s: relation cycle", e.ID())
			}
			seen[r.Base] = true
		}
	}
	return c, nil
}

// Entry returns the entry for id.
func (c *Catalogue) Entry(id ID) (Entry, bool) {
	i, ok := c.byID[id]
	if !ok {
		return Entry{}, false
	}
	return c.entries[i], true
}

// Entries returns all entries: settings, then flags, then RAM variables,
// each by number.
func (c *Catalogue) Entries() []Entry { return append([]Entry(nil), c.entries...) }

// FlagLocation returns the setting that holds flag n and its bit number.
func (c *Catalogue) FlagLocation(n uint16) (word uint16, bit uint, ok bool) {
	if int(n/16) >= len(c.flagWords) {
		return 0, 0, false
	}
	return c.flagWords[n/16], uint(n % 16), true
}

// IsFlagWord reports whether setting id holds flag bits.
func (c *Catalogue) IsFlagWord(id uint16) bool {
	for _, w := range c.flagWords {
		if w == id {
			return true
		}
	}
	return false
}

// flagsIn returns the flag numbers held by setting word, or nil.
func (c *Catalogue) flagsIn(word uint16) (first uint16, ok bool) {
	for i, w := range c.flagWords {
		if w == word {
			return uint16(i * 16), true
		}
	}
	return 0, false
}

var defaultCatalogue = func() *Catalogue {
	c, err := NewCatalogue(defaultFlagWords, defaultEntries)
	if err != nil {
		panic(err)
	}
	return c
}()

// DefaultCatalogue returns the built-in catalogue (see catalogue_data.go).
func DefaultCatalogue() *Catalogue { return defaultCatalogue }
