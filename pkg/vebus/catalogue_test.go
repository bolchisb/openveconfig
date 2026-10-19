package vebus

import (
	"strings"
	"testing"
)

func TestDefaultCatalogue(t *testing.T) {
	c := DefaultCatalogue()
	// Every documented setting 0-65 and RAM variable 0-19 is in it.
	for n := uint16(0); n <= 65; n++ {
		if _, ok := c.Entry(SettingID(n)); !ok {
			t.Errorf("setting %d missing", n)
		}
	}
	for n := uint16(0); n <= 19; n++ {
		if _, ok := c.Entry(RAMID(n)); !ok {
			t.Errorf("RAM %d missing", n)
		}
	}
	// Flags 0-34 are named in §7.3.13.3/5.
	for n := uint16(0); n <= 34; n++ {
		if _, ok := c.Entry(FlagID(n)); !ok {
			t.Errorf("flag %d missing", n)
		}
	}
	for _, w := range []uint16{0, 1, 60, 61, 82} {
		if !c.IsFlagWord(w) {
			t.Errorf("setting %d is not a flag word", w)
		}
	}
	if c.IsFlagWord(2) {
		t.Error("setting 2 is a flag word")
	}
}

func TestFlagLocation(t *testing.T) {
	c := DefaultCatalogue()
	for _, tc := range []struct {
		n    uint16
		word uint16
		bit  uint
	}{
		{0, 0, 0}, {3, 0, 3}, {15, 0, 15}, // Flags[0] is bit 0 in Flags0 (§7.3.13.3)
		{16, 1, 0}, {31, 1, 15}, // Flags[31] is bit 15 in Flags1
		{32, 60, 0}, {36, 60, 4}, {48, 61, 0}, {64, 82, 0}, {79, 82, 15},
	} {
		w, b, ok := c.FlagLocation(tc.n)
		if !ok || w != tc.word || b != tc.bit {
			t.Errorf("FlagLocation(%d) = %d, %d, %v; want %d, %d", tc.n, w, b, ok, tc.word, tc.bit)
		}
	}
	if _, _, ok := c.FlagLocation(80); ok {
		t.Error("flag 80 has a location")
	}
}

// TestCataloguePolicy checks the catalogue against docs/CLEAN-ROOM.md: every
// entry cites an allowed source, and no name carries an identifier style of
// the original tools' binaries.
func TestCataloguePolicy(t *testing.T) {
	allowed := []string{"MK2 Protocol 3.14 §7.3.13.", "community: xcellsior/ve-bus-programming FINDINGS.md §12.", "own name; "}
	// Identifier styles that must not appear (split so this file does not
	// contain them either).
	forbidden := []string{"EP" + "ROM_", "EB" + "IT_", "Ba" + "re", "FU" + "N_", "Tab" + "Sheet", ":\\", "0x00"}
	for _, e := range DefaultCatalogue().Entries() {
		ok := false
		for _, a := range allowed {
			ok = ok || strings.HasPrefix(e.Source, a)
		}
		if !ok {
			t.Errorf("%s: source %q is not an allowed source", e.ID(), e.Source)
		}
		for _, f := range forbidden {
			if strings.Contains(e.Name, f) || strings.Contains(e.Group, f) {
				t.Errorf("%s: %q contains %q", e.ID(), e.Name, f)
			}
		}
		if e.Kind == RAM && e.Tab != TabMonitor {
			t.Errorf("%s: RAM variable not on the Monitor tab", e.ID())
		}
		if e.FlagWord && e.Tab != TabNone {
			t.Errorf("%s: flag word on tab %s", e.ID(), e.Tab)
		}
		if e.Group == "" {
			t.Errorf("%s: no group", e.ID())
		}
	}
}

func TestNewCatalogueRejects(t *testing.T) {
	word := Entry{Kind: Setting, Num: 0, Name: "w", Source: "s", FlagWord: true}
	base := Entry{Kind: Setting, Num: 1, Name: "b", Source: "s"}
	for name, tc := range map[string]struct {
		words   []uint16
		entries []Entry
	}{
		"duplicate":        {nil, []Entry{base, base}},
		"no source":        {nil, []Entry{{Kind: Setting, Num: 1, Name: "x"}}},
		"bad kind":         {nil, []Entry{{Num: 1, Name: "x", Source: "s"}}},
		"bad tab":          {nil, []Entry{{Kind: Setting, Num: 1, Name: "x", Source: "s", Tab: TabMonitor + 1}}},
		"flag outside":     {[]uint16{0}, []Entry{word, {Kind: Flag, Num: 16, Name: "f", Source: "s"}}},
		"word not marked":  {[]uint16{1}, []Entry{base}},
		"unknown relation": {nil, []Entry{{Kind: Setting, Num: 2, Name: "r", Source: "s", RelativeTo: &Relative{Base: 9, Sign: 1}}}},
		"bad sign":         {nil, []Entry{base, {Kind: Setting, Num: 2, Name: "r", Source: "s", RelativeTo: &Relative{Base: 1, Sign: 2}}}},
		"cycle": {nil, []Entry{
			{Kind: Setting, Num: 1, Name: "a", Source: "s", RelativeTo: &Relative{Base: 2, Sign: 1}},
			{Kind: Setting, Num: 2, Name: "b", Source: "s", RelativeTo: &Relative{Base: 1, Sign: 1}},
		}},
		"unknown inverse": {[]uint16{0}, []Entry{word, {Kind: Flag, Num: 1, Name: "f", Source: "s", InverseOf: u16p(5)}}},
	} {
		if _, err := NewCatalogue(tc.words, tc.entries); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func u16p(v uint16) *uint16 { return &v }

// TestCatalogueExtension shows that a catalogue grows by data alone: a new
// entry is shown for a device that supports it, with no other change.
func TestCatalogueExtension(t *testing.T) {
	entries := append(DefaultCatalogue().Entries(),
		Entry{Kind: Setting, Num: 150, Name: "Extra", Unit: "V", Tab: TabAdvanced, Group: "Extra", Source: "own name; test"})
	c, err := NewCatalogue(defaultFlagWords, entries)
	if err != nil {
		t.Fatal(err)
	}
	d := NewDevice(c)
	d.AddSetting(150, settingInfo(-100, 0, 100, 0, 1000), 250)
	es := d.Entries()
	if len(es) != 1 || es[0].Name != "Extra" {
		t.Fatalf("Entries = %+v", es)
	}
	if v, _ := d.Display(SettingID(150)); v != 2.5 {
		t.Errorf("Display = %v", v)
	}
}
