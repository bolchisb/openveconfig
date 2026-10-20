package gridcode

import (
	"fmt"
	"math"
	"math/bits"
	"slices"
	"testing"
)

// put stores field f under p's mask (and its twin) in word, as the device
// would hold it.
func put(p Param, word uint16, f int) uint16 {
	word = word&^p.Mask | uint16(f)<<p.shift()&p.Mask
	return word&^p.Twin | uint16(f)<<bits.TrailingZeros16(p.Twin)&p.Twin
}

// fieldValues returns the field values a round trip covers for p.
func fieldValues(p Param) []int {
	switch p.Kind {
	case Checkbox:
		if p.Values != nil {
			return p.Values[:]
		}
		return []int{0, int(p.Mask >> p.shift())}
	case Choice:
		var fs []int
		for f := range p.Choices {
			fs = append(fs, f)
		}
		return fs
	}
	if p.TanPhi {
		return []int{p.Min, p.Max, int(math.Round(math.Tan(math.Acos(0.5)) * 256))}
	}
	if p.CosPhiMin != 0 {
		// Fields the original writes: 1 and the lowest of either sign.
		return []int{p.Max, int(math.Round((2 - p.CosPhiMin) * 32768)), int(math.Round(p.CosPhiMin * 32768))}
	}
	if p.TripDelay {
		// The display steps are coarser than the field: only the fields the
		// original writes come back the same.
		return []int{p.Min, 4, p.Max, p.Default}
	}
	fs := []int{p.Min, p.Max, (p.Min + p.Max) / 2}
	if p.HasDefault {
		fs = append(fs, p.Default)
	}
	return fs
}

var otherBits = []uint16{0x0000, 0xFFFF, 0xA5C3, 0x5A3C}

// testPages returns Pages() and each page that has a reactive-power group
// with each layout of it.
func testPages() []*Page {
	pages := Pages()
	for _, p := range reactivePages {
		for n := range 16 {
			l := reactiveLayout{compact: n&1 != 0, volts: n&2 != 0, filterQ: n&4 != 0, lockU: n&8 != 0, lockKnown: true, lowest: 0.9}
			if l.compact && l.volts {
				continue // the volt codes always have layout A
			}
			ps, _ := reactiveParams(l)
			q := *p
			q.Name += fmt.Sprintf(" with reactive power %+v", l)
			q.Params = append(slices.Clone(p.Params), ps...)
			pages = append(pages, &q)
		}
	}
	return pages
}

func TestRoundTrip(t *testing.T) {
	for _, pg := range testPages() {
		for _, p := range pg.Params {
			if p.High != 0 {
				continue // TestTwoWordField
			}
			for _, f := range fieldValues(p) {
				for _, base := range otherBits {
					w := put(p, base, f)
					if got := p.Field(w); got != f {
						t.Errorf("%s / %s: Field(%#04x) = %d, want %d", pg.Name, p.Name, w, got, f)
						continue
					}
					v := p.Unpack(w)
					// Device word -> display value -> device word is identical.
					got, err := p.Pack(w, v)
					if err != nil || got != w {
						t.Errorf("%s / %s: Pack(%#04x, %g) = %#04x, %v; want %#04x", pg.Name, p.Name, w, v, got, err, w)
					}
					// Packing into any word changes only the field's bits.
					got, err = p.Pack(^base, v)
					if m := p.Mask | p.Twin; err != nil || got&^m != ^base&^m || p.Field(got) != f {
						t.Errorf("%s / %s: Pack(%#04x, %g) = %#04x, %v", pg.Name, p.Name, ^base, v, got, err)
					}
				}
			}
		}
	}
}

func TestOutOfRange(t *testing.T) {
	const w = 0x1234
	for _, pg := range testPages() {
		for _, p := range pg.Params {
			var bad []float64
			switch p.Kind {
			case Number:
				lo, hi := min(p.Min, p.Max), max(p.Min, p.Max)
				bad = []float64{p.Value(lo - 1), p.Value(hi + 1), math.NaN()}
				if p.TripDelay {
					bad = []float64{-0.5, 180.5, math.NaN()}
				}
				if p.CosPhiMin != 0 {
					bad = []float64{-1.01, 1.01, math.NaN()}
				}
				if p.TanPhi {
					bad = []float64{-0.01, 1.01, math.NaN()}
				}
			case Checkbox:
				bad = []float64{-1, 0.5, 2}
			case Choice:
				m := 0
				for f := range p.Choices {
					m = max(m, f)
				}
				bad = []float64{-1, 0.5, float64(m + 1)}
			}
			for _, v := range bad {
				got, err := p.Pack(w, v)
				if err == nil || got != w {
					t.Errorf("%s / %s: Pack(%g) = %#04x, %v; want an error and the word unchanged", pg.Name, p.Name, v, got, err)
				}
			}
		}
	}
}

// TestKnownValues checks the conversions against values from the
// behavioural spec.
func TestKnownValues(t *testing.T) {
	// The Europe page as the code of a table entry with the lock-in bit
	// shows it.
	euLock := pageEurope.WithReactivePower(18, TableModel(withEntry(maskTable(), 18, 1)))
	find := func(pg *Page, name string) Param {
		t.Helper()
		for _, p := range pg.Params {
			if p.Name == name {
				return p
			}
		}
		t.Fatalf("%s: no parameter %q", pg.Name, name)
		return Param{}
	}
	for _, tc := range []struct {
		pg   *Page
		name string
		v    float64
		word uint16 // with other bits clear
	}{
		{pageUK, "Over-frequency power reduction: start frequency", 50.3997, 43293},
		{pageItaly, "Power limit PCMax (CEI 0-21 term)", 1250, 64286},
		{pageItaly, "Power limit PCMax (CEI 0-21 term)", 100, 65436},
		{pageRomania, "Reconnection wait time", 900, 64650},
		{pageRomania, "Under-voltage trip delay", 0, 3}, // a real file; the original shows 0.0
		{pageRomania, "Under-voltage trip delay", 0.5, 4},
		{pageAustria, "Fast over-voltage trip delay", 180, 6851},
		{pageEurope, "After trip: wait time before reconnecting", 60, 0xFD2},   // 14 - 60 = -46
		{pageEurope, "After trip: wait time before reconnecting", 1200, 0xB5E}, // 14 - 1200 = -1186
		{pageEurope, "After trip: lowest frequency allowed to reconnect", 49.5, 20},
		{pageEurope, "After trip: highest frequency allowed to reconnect", 50.2, 8 << 8},
		{pageEurope, "P(U) point 1: voltage", 90, 0xD8}, // -40
		{pageEurope, "Over-frequency response: start delay", 3.1, 31 << 11},
		{euLock, "Voltage threshold to enable the curve (lock-in)", 70, 0x8800}, // -120
		{euLock, "Reactive-power response time constant", 3.0002, 4581},
		{pageAustria, "P(U) curve: voltage span to zero power", 4.6, 18236},
		{pageAUNZM, "P(U) point 1: charge power limit", 20, 148 << 8},
		{pageMicrogrid, "Active power at reference frequency (P0)", -100, 1024},
		{pageMicrogrid, "Fixed black-start retry delay", 0, 0xFF00},
		{pageAUNZ2015, "Demand-response mode 0 enabled", 1, 1},
		{pagePoland, "Over-frequency power reduction: start frequency", 50.35, 0x0E0E}, // both bytes
		{pagePoland, "Limited frequency-sensitive mode (over-frequency) enabled", 0, 0xFFFF},
		{pagePoland, "Limited frequency-sensitive mode (over-frequency) enabled", 1, 0x0808},
		{pageUK, "Use auxiliary input 1 as feed-in inhibit", 1, 0}, // inverted
		{pageUK, "Use auxiliary input 1 as feed-in inhibit", 0, 4},
	} {
		p := find(tc.pg, tc.name)
		got, err := p.Pack(0, tc.v)
		if err != nil || got != tc.word {
			t.Errorf("%s: Pack(0, %g) = %#04x, %v; want %#04x", tc.name, tc.v, got, err, tc.word)
		}
		if v := p.Unpack(tc.word); math.Abs(v-tc.v) > 1e-4*math.Max(1, math.Abs(tc.v)) {
			t.Errorf("%s: Unpack(%#04x) = %g, want %g", tc.name, tc.word, v, tc.v)
		}
	}
}

// TestEnabled checks when the original greys a parameter.
func TestEnabled(t *testing.T) {
	pu := Param{NeedsWord: 148, NeedsMask: 0x18}
	lock := Param{NeedsWord: 148, NeedsMask: 0x20, NeedsClear: true}
	start := Param{}.lfsmo()
	for _, tc := range []struct {
		p    Param
		w    uint16
		want bool
	}{
		{Param{}, 0, true}, {pu, 0xFFE7, false}, {pu, 0x08, true}, {lock, 0x20, false}, {lock, 0xFFDF, true},
		{start, 0xFFFF, false}, {start, 0xFF00, false}, {start, 0x0808, true}, {start, 0xFE00, true},
	} {
		if got := tc.p.Enabled(tc.w); got != tc.want {
			t.Errorf("%+v: Enabled(%#04x) = %t, want %t", tc.p, tc.w, got, tc.want)
		}
	}
}

// TestTables checks the tables for entry mistakes.
func TestTables(t *testing.T) {
	for _, pg := range testPages() {
		for _, p := range pg.Params {
			name := pg.Name + " / " + p.Name
			if p.Word < 129 || p.Word > 190 {
				t.Errorf("%s: word %d outside 129-190", name, p.Word)
			}
			m := p.Mask >> p.shift()
			if p.Mask == 0 || m&(m+1) != 0 {
				t.Errorf("%s: mask %#04x is not one run of bits", name, p.Mask)
			}
			w := int(p.width())
			if p.High != 0 {
				h := p.HighMask >> bits.TrailingZeros16(p.HighMask)
				if p.HighMask == 0 || h&(h+1) != 0 || p.High < 129 || p.High > 190 || p.High == p.Word {
					t.Errorf("%s: invalid two-word field (high word %d, mask %#04x)", name, p.High, p.HighMask)
				}
				w += bits.OnesCount16(p.HighMask)
			}
			lo, hi := 0, 1<<w-1
			if p.Signed {
				lo, hi = -1<<(w-1), 1<<(w-1)-1
			}
			fits := func(f int) bool { return f >= lo && f <= hi }
			switch p.Kind {
			case Number:
				if p.Scale == 0 {
					t.Errorf("%s: no scale", name)
				}
				if !fits(p.Min) || !fits(p.Max) {
					t.Errorf("%s: limits %d, %d do not fit the field", name, p.Min, p.Max)
				}
				if p.HasDefault && (p.Default < min(p.Min, p.Max) || p.Default > max(p.Min, p.Max)) {
					t.Errorf("%s: default %d outside its limits", name, p.Default)
				}
			case Checkbox:
				if p.Values != nil {
					if !fits(p.Values[0]) || !fits(p.Values[1]) || p.Values[0] == p.Values[1] {
						t.Errorf("%s: checkbox values %v", name, *p.Values)
					}
				} else if bits.OnesCount16(p.Mask) != 1 {
					t.Errorf("%s: checkbox mask %#04x is not one bit", name, p.Mask)
				}
				if p.HasDefault && p.Default != 0 && p.Default != 1 {
					t.Errorf("%s: checkbox default %d", name, p.Default)
				}
			case Choice:
				for f := range p.Choices {
					if !fits(f) {
						t.Errorf("%s: choice %d does not fit the field", name, f)
					}
				}
				if _, ok := p.Choices[p.Default]; p.HasDefault && !ok {
					t.Errorf("%s: default %d is not a choice", name, p.Default)
				}
			default:
				t.Errorf("%s: kind %d", name, p.Kind)
			}
		}
	}
}

// TestNoOverlap checks that no two parameters of a page share bits of a
// word, apart from parameters that are never shown together (they follow
// different regulation modes).
func TestNoOverlap(t *testing.T) {
	// modes returns the regulation modes p is shown with.
	modes := func(p Param) (m [4]bool) {
		for v := range 4 {
			m[v] = p.ShowMask == 0 || p.ShowWord == 148 && p.Shown(uint16(v))
		}
		return m
	}
	together := func(p, q Param) bool {
		a, b := modes(p), modes(q)
		for v := range 4 {
			if a[v] && b[v] {
				return true
			}
		}
		return false
	}
	// parts returns the bits of each word p is stored in.
	parts := func(p Param) map[uint16]uint16 {
		m := map[uint16]uint16{p.Word: p.Mask}
		if p.High != 0 {
			m[p.High] |= p.HighMask
		}
		return m
	}
	// switches reports whether p is a box that switches q off through q's
	// own field (Param.Values).
	switches := func(p, q Param) bool {
		return p.Values != nil && p.Word == q.Word && p.Mask == q.Mask && p.Twin == q.Twin
	}
	for _, pg := range testPages() {
		for i, p := range pg.Params {
			for _, q := range pg.Params[i+1:] {
				if switches(p, q) || switches(q, p) {
					continue
				}
				for w, m := range parts(p) {
					if shared := m & parts(q)[w]; shared != 0 && together(p, q) {
						t.Errorf("%s: %q and %q share bits %#04x of word %d", pg.Name, p.Name, q.Name, shared, w)
					}
				}
			}
		}
	}
}

// TestSplitField checks fields split over the high bits of one word and
// the bits of another, with made-up words.
func TestSplitField(t *testing.T) {
	signed := Param{Name: "signed", Kind: Number, Word: 129, Mask: 0xF000, High: 130, HighMask: 0xF000,
		Signed: true, Scale: 1, Min: -128, Max: 127}
	unsigned := Param{Name: "unsigned", Kind: Number, Word: 133, Mask: 0xFF00, High: 132, HighMask: 0xF000,
		Scale: 1, Max: 4095}
	for _, tc := range []struct {
		p                Param
		word, high       uint16
		field            int
		wantWord, wantHi uint16 // after packing the field into 0x0ABC, 0x0DEF
	}{
		{signed, 0x3000, 0x2000, 0x23, 0x3ABC, 0x2DEF},
		{signed, 0xFFFF, 0xEFFF, -1 - 0x10, 0xFABC, 0xEDEF},
		{signed, 0x0FFF, 0x8FFF, -128, 0x0ABC, 0x8DEF},
		{unsigned, 0x5800, 0x2000, 0x258, 0x58BC, 0x2DEF},
		{unsigned, 0x00FF, 0x0FFF, 0, 0x00BC, 0x0DEF},
	} {
		if f := tc.p.FieldWords(tc.word, tc.high); f != tc.field {
			t.Errorf("%s: FieldWords(%#04x, %#04x) = %d, want %d", tc.p.Name, tc.word, tc.high, f, tc.field)
		}
		w, h, err := tc.p.PackWords(0x0ABC, 0x0DEF, tc.p.Value(tc.field))
		if err != nil || w != tc.wantWord || h != tc.wantHi {
			t.Errorf("%s: PackWords(%d) = %#04x, %#04x, %v; want %#04x, %#04x", tc.p.Name, tc.field, w, h, err, tc.wantWord, tc.wantHi)
		}
	}
	if w, h, err := unsigned.PackWords(1, 2, 4096); err == nil || w != 1 || h != 2 {
		t.Errorf("PackWords(4096) = %d, %d, %v; want an error and the words unchanged", w, h, err)
	}
	// Every split field of the pages comes back the same through its words.
	for _, pg := range testPages() {
		for _, p := range pg.Params {
			if p.High == 0 {
				continue
			}
			for _, f := range fieldValues(p) {
				w, h, err := p.PackWords(0x5A5A, 0xA5A5, p.Value(f))
				if err != nil || p.FieldWords(w, h) != f || w&^p.Mask != 0x5A5A&^p.Mask || h&^p.HighMask != 0xA5A5&^p.HighMask {
					t.Errorf("%s / %s: field %d packed as %#04x, %#04x, %v", pg.Name, p.Name, f, w, h, err)
				}
			}
		}
	}
}

func TestCodes(t *testing.T) {
	cs := Codes()
	if len(cs) != 33 {
		t.Fatalf("%d codes, want 33", len(cs))
	}
	for i, c := range cs {
		if c.Code != Code(i) {
			t.Errorf("entry %d has code %d", i, c.Code)
		}
		if got, ok := Lookup(c.Code); !ok || got != c {
			t.Errorf("Lookup(%d) = %v, %v", c.Code, got, ok)
		}
	}
	if _, ok := Lookup(33); ok {
		t.Error("Lookup(33) found a code")
	}
}

// listEntry is a made-up grid-info entry for listTable: its version, its
// flags and the grid words it uses besides 128 and 191 (nil: an empty
// entry).
type listEntry struct {
	version byte
	flags   uint16
	words   []uint16
}

// listTable builds a format-3 grid-info table of n grid codes; codes
// without an entry in entries get an empty one.
func listTable(n int, entries map[Code]listEntry) []byte {
	b := []byte{0, 0, 3, byte(n), 8}
	var body []byte
	for c := range Code(n) {
		off := tableOffOffsets + 2*n + len(body)
		b = append(b, byte(off), byte(off>>8))
		e := entries[c]
		used := make([]byte, 8)
		if e.words != nil {
			for _, w := range append([]uint16{128, 191}, e.words...) {
				used[(w-128)/8] |= 1 << ((w - 128) % 8)
			}
		}
		body = append(body, e.version, byte(e.flags), byte(e.flags>>8))
		body = append(body, make([]byte, 8)...)
		body = append(body, used...)
		if e.words != nil {
			body = append(body, make([]byte, 2*len(e.words))...) // defaults
		}
		body = append(body, 0)
	}
	return append(b, body...)
}

func TestLookupFor(t *testing.T) {
	const fw, oldFW = 2600480, 2600449 // revisions 480 and 449
	listed := []uint16{140}
	table := func(entries map[Code]listEntry) Model {
		t.Helper()
		m := TableModel(listTable(32, entries))
		if !m.Known {
			t.Fatal("table refused")
		}
		return m
	}
	std := func(m Model, c Code, firmware uint32) string {
		ci, ok := LookupFor(c, m, firmware)
		if !ok {
			t.Fatalf("LookupFor(%d) found no code", c)
		}
		return ci.Standard
	}
	static := func(c Code) string { ci, _ := Lookup(c); return ci.Standard }
	m := table(map[Code]listEntry{
		2:  {0, 0, []uint16{148}},
		4:  {0, 0, []uint16{158}},
		6:  {1, 0, listed},
		7:  {1, 0x8000, listed},
		9:  {1, 0, listed},
		11: {0, 0, listed},
		12: {1, 0, listed},
		22: {1, 0, listed},
	})
	for _, tc := range []struct {
		m    Model
		c    Code
		fw   uint32
		want string
	}{
		{m, 2, fw, "G98/1 March 2019, G99/1 May 2018"},
		{table(map[Code]listEntry{2: {1, 0, []uint16{148}}}), 2, fw, "G98/1 Amendment 6, G99/1 Amendment 8"},
		{table(map[Code]listEntry{2: {2, 0, []uint16{148}}}), 2, fw, static(2)},
		{table(map[Code]listEntry{2: {0, 0, listed}}), 2, fw, static(2)}, // word 148 not used
		{TableModel(listTable(33, map[Code]listEntry{2: {1, 0, []uint16{148}}, 31: {0, 0, listed}})), 2, fw, "G99/1 Amendment 8"},
		{m, 4, fw, "VDE-AR-N 4105:2018-11, external NS protection"},
		{m, 5, fw, "VDE-AR-N 4105:2018-11, internal NS protection"},
		{table(nil), 5, fw, static(5)},
		{m, 6, fw, "AS/NZS 4777.2:2015 (AC Neutral path not externally joined)"},
		{m, 7, fw, static(7)}, // flag bit 15 set
		{table(nil), 6, fw, static(6)},
		{m, 9, fw, "VDE V 0126-1-1 VFR 2019"},
		{table(nil), 9, fw, static(9)},
		{m, 11, fw, "C10/11:2012-04"},
		{table(map[Code]listEntry{11: {2, 0, listed}}), 11, fw, "C10/11 ed. 2.1:2019-09"},
		{table(nil), 11, fw, static(11)}, // not in the table
		{m, 12, fw, "TOR-Erzeuger A V1.1:2019-12"},
		{m, 12, oldFW, static(12)},
		{table(nil), 12, fw, "TOR-D4 V2.3:2016-7"},
		{table(map[Code]listEntry{12: {0, 0, listed}}), 12, fw, "TOR-D4 V2.3:2016-7"},
		{m, 22, fw, static(22) + " (no N-bypass)"},
		{m, 23, fw, static(23)}, // not in the table
		// Without a table: codes 0-4 are known, from 13 not.
		{TableModel(nil), 2, fw, static(2)},
		{TableModel(nil), 6, fw, static(6)},
		{TableModel(nil), 11, fw, static(11)},
		{TableModel(nil), 12, fw, "TOR-D4 V2.3:2016-7"},
		{TableModel(nil), 22, fw, static(22)},
		// A table that could not be read.
		{Model{}, 12, fw, static(12)},
		{Model{}, 22, fw, static(22)},
	} {
		if got := std(tc.m, tc.c, tc.fw); got != tc.want {
			t.Errorf("table %x, code %d, firmware %d: %q, want %q", tc.m.Table, tc.c, tc.fw, got, tc.want)
		}
	}
}

func TestSelectableWithoutPassword(t *testing.T) {
	for _, tc := range []struct {
		c    Code
		fw   uint32
		want bool
	}{
		{0, 2699000, true},
		{0, 0, true},
		{1, 2699414, false},
		{1, 2699415, true},
		{1, 1415, true}, // revision is the version mod 1000
		{21, 2699415, false},
		{21, 2699553, false},
		{21, 2699554, true},
		{2, 2699999, false},
		{17, 2699554, false},
		{32, 2699999, false},
	} {
		if got := SelectableWithoutPassword(tc.c, tc.fw); got != tc.want {
			t.Errorf("SelectableWithoutPassword(%d, %d) = %v", tc.c, tc.fw, got)
		}
	}
}

func TestPageFor(t *testing.T) {
	// Not known: codes outside the list, the AS/NZS 4777.2:2015 codes
	// without a version.
	for _, c := range []Code{33, 0xFFFF, 6, 16, 9, 11, 12} {
		if p, known := PageFor(c, -1); known || p != nil {
			t.Errorf("PageFor(%d, -1) = %v, %v; want unknown", c, p, known)
		}
	}
	for _, c := range []Code{0, 1, 21} {
		if p, known := PageFor(c, -1); !known || p != nil {
			t.Errorf("PageFor(%d) = %v, %v; want no page", c, p, known)
		}
	}
	// The original's choice.
	for _, tc := range []struct {
		c    Code
		v    int
		want *Page
	}{
		{2, -1, pageUK}, {20, -1, pageUK}, {31, -1, pageUK}, {3, -1, pageItaly},
		{4, -1, pageGermanyA}, {5, -1, pageGermanyB},
		{6, 0, pageAUNZ2015}, {16, 0, pageAUNZ2015}, {7, 1, pageAUNZ2015B}, {15, 2, pageAUNZ2015B},
		{9, 0, pageFrance}, {9, 1, pageFrance}, {9, 2, pageEurope},
		{11, 0, pageBelgium}, {11, 1, pageEurope}, {12, 0, pageAustria}, {12, 2, pageEurope},
		{10, -1, pageSpain}, {17, -1, pageEurope}, {18, -1, pageEurope}, {19, -1, pageEurope}, {30, -1, pageEurope},
		{22, -1, pageAUNZM}, {25, -1, pageAUNZM}, {29, -1, pageAUNZM}, {32, -1, pagePoland},
	} {
		if p, known := PageFor(tc.c, tc.v); !known || p != tc.want {
			t.Errorf("PageFor(%d, %d) = %v, %v; want %s", tc.c, tc.v, p, known, tc.want.Name)
		}
	}
	// Every grid code's page is known once its version is.
	for _, ci := range Codes() {
		if _, known := PageFor(ci.Code, 0); !known {
			t.Errorf("code %d: page not known", ci.Code)
		}
	}
	// Every mapped page is listed by Pages.
	listed := map[*Page]bool{}
	for _, p := range Pages() {
		listed[p] = true
	}
	for c, p := range pageOf {
		if p != nil && !listed[p] {
			t.Errorf("code %d: page %s not in Pages", c, p.Name)
		}
	}
}

// TestTwoWordField checks a value held in two words against a real file,
// where VEConfigure shows 255.3 V for 59728 in setting 129 and 77 in 130.
func TestTwoWordField(t *testing.T) {
	i := slices.IndexFunc(pageRomania.Params, func(p Param) bool { return p.Name == overVoltage10Min })
	if i < 0 {
		t.Fatal("Romania has no over-voltage parameter")
	}
	p := pageRomania.Params[i]
	if v := p.UnpackWords(59728, 77); math.Abs(v-255.3) > 1e-9 {
		t.Errorf("UnpackWords = %g, want 255.3", v)
	}
	if lo, hi, err := p.PackWords(0, 0, 255.3); err != nil || lo != 59728 || hi != 77 {
		t.Errorf("PackWords(255.3) = %d, %d, %v", lo, hi, err)
	}
	if lo, hi, err := p.PackWords(1, 2, 300); err == nil || lo != 1 || hi != 2 {
		t.Errorf("PackWords(300) = %d, %d, %v; want an error and the words unchanged", lo, hi, err)
	}
	if _, err := p.Pack(0, 255.3); err == nil {
		t.Error("Pack accepted a two-word parameter")
	}
}

// TestReactivePower checks which layout of the reactive-power group each
// grid code gets, as the original chooses it, and the values of a real
// Romania file (layout A, regulation 0).
func TestReactivePower(t *testing.T) {
	for _, c := range []struct {
		code           Code
		v              int
		compact, known bool
	}{
		{9, 1, false, true}, {9, 2, true, true}, {9, -1, false, false},
		{11, 0, false, true}, {11, 1, true, true}, {12, 1, true, true}, {12, -1, false, false},
		{17, -1, true, true}, {14, -1, false, true}, {2, 0, false, true},
	} {
		if compact, known := compactReactive(c.code, c.v); compact != c.compact || known != c.known {
			t.Errorf("code %d version %d: compact %t known %t, want %t %t", c.code, c.v, compact, known, c.compact, c.known)
		}
	}
	has := func(p *Page, name string) bool {
		return slices.ContainsFunc(p.Params, func(q Param) bool { return q.Name == name })
	}
	param := func(p *Page, name string) Param {
		t.Helper()
		i := slices.IndexFunc(p.Params, func(q Param) bool { return q.Name == name })
		if i < 0 {
			t.Fatalf("%s: no %q", p.Name, name)
		}
		return p.Params[i]
	}
	const (
		mode, filter = "Reactive-power control mode", "Reactive-power response time constant"
		limit, lockU = "Active-power threshold to enable the curve (lock-in)", "Apply voltage lock-in/lock-out"
	)
	none := TableModel(nil)
	device := Model{}
	if p := pageRomania.WithReactivePower(14, none); !has(p, mode) || has(p, limit) || has(pageRomania, mode) {
		t.Error("Romania: want layout A on a copy of the page")
	}
	if p := pageEurope.WithReactivePower(17, none); !has(p, limit) || has(p, lockU) {
		t.Error("Europe without a table: want layout B with the limitation box, without the voltage lock-in")
	}
	// maskTable's code 1 has the lock-in bit in word 148's mask; any code
	// with layout B shows the box then.
	if p := pageEurope.WithReactivePower(18, TableModel(withEntry(maskTable(), 18, 1))); !has(p, lockU) {
		t.Error("lock-in bit in the table: want the voltage lock-in box")
	}
	if p := pageEurope.WithReactivePower(17, device); !slices.Contains(p.Unsupported, lockU) {
		t.Error("Europe from a device: want the voltage lock-in listed as not supported")
	}
	if p := pageFrance.WithReactivePower(9, device); has(p, mode) || !slices.Contains(p.Unsupported, ReactivePowerUnknownLayout) {
		t.Error("France from a device: want the group listed as not supported")
	}
	if p := pageAUNZ2015.WithReactivePower(6, none); p != pageAUNZ2015 {
		t.Error("AS/NZS 4777.2:2015, version 0: the page has no reactive-power group")
	}
	// The filter time inside the Q(U) settings: Spain, France before
	// version 2, Belgium and Austria at version 0, Germany without word 158.
	for _, tc := range []struct {
		c      Code
		inside bool
	}{{10, true}, {9, true}, {11, true}, {12, true}, {4, true}, {5, true}, {14, false}, {17, false}} {
		p, _ := PageOf(tc.c, none)
		if f := param(p, filter); f.Shown(0) == tc.inside || !f.Shown(3) {
			t.Errorf("code %d: filter time inside the Q(U) settings %t, want %t", tc.c, !f.Shown(0), tc.inside)
		}
	}
	// AS/NZS 4777.2:2015 from parameter set 1: Q(U) voltages in volts.
	if p, _ := PageOf(6, TableModel(gridTable(3, []byte{0, 1}, []int{0, 0, 0, 0, 0, 0, 1}))); param(p, "Q(U) point 1: voltage").Unit != "V" {
		t.Error("code 6, version 1: want the Q(U) voltages in volts")
	}
	if p, _ := PageOf(18, none); p.Title != "DK1 (Western Denmark)" || p.Name != pageEurope.Name {
		t.Errorf("code 18: page %q titled %q", p.Name, p.Title)
	}

	p, _ := PageOf(14, none)
	if v := param(p, filter).Unpack(4165); math.Abs(v-3.3) > 0.001 {
		t.Errorf("filter time %g, want 3.3", v)
	}
	if v := param(p, "cos-phi(P) point 2: active power").Unpack(9830); math.Abs(v-30) > 0.01 {
		t.Errorf("cos phi(P) point 2 at %g %%, want 30", v)
	}
	if q := param(p, "cos-phi(P) point 2: active power"); q.Shown(0) || !q.Shown(1) || !q.Shown(0xFFF5) {
		t.Error("cos phi(P) points not shown with regulation 1 only")
	}
	// The cos phi conversion of the original: 32768 is 1.00 (the Romania
	// file), above it a positive cos phi, below it a negative one, and a
	// cos phi nearer to 0 than the code's lowest is written as the lowest.
	fixed := param(p, "Constant cos phi (fixed mode)")
	for _, c := range []struct {
		v    float64
		word uint16
		back float64
	}{
		{1, 32768, 1}, {-1, 32768, 1}, {0.95, 34406, 0.95}, {-0.95, 31130, -0.95},
		{0.5, 36045, 0.9}, {-0.5, 29491, -0.9}, {0, 36045, 0.9},
	} {
		w, err := fixed.Pack(0, c.v)
		if err != nil || w != c.word {
			t.Errorf("cos phi %g: Pack = %d, %v; want %d", c.v, w, err, c.word)
		}
		if v := fixed.Unpack(w); v != c.back {
			t.Errorf("cos phi %g: Unpack(%d) = %g, want %g", c.v, w, v, c.back)
		}
	}
	for _, c := range []struct {
		code Code
		v    int
		want float64
	}{{14, 0, 0.9}, {2, 0, 0.8}, {17, 0, 0.8}, {9, 1, 0.9}, {9, 2, 0.8}, {12, 0, 0.9}, {12, 1, 0.8}} {
		if got := lowestCosPhi(c.code, c.v); got != c.want {
			t.Errorf("code %d version %d: lowest cos phi %g, want %g", c.code, c.v, got, c.want)
		}
	}
	// Min. Cos phi: tan(phi) * 256, 0xFFFF for 0.
	eu, _ := PageOf(17, none)
	lowest := param(eu, "Lowest cos phi allowed while following a Q set-point")
	for _, c := range []struct {
		v    float64
		word uint16
	}{{0, 0xFFFF}, {1, 0}, {0.9, 124}, {0.5, 443}} {
		if w, err := lowest.Pack(0, c.v); err != nil || w != c.word {
			t.Errorf("Min. Cos phi %g: Pack = %d, %v; want %d", c.v, w, err, c.word)
		}
		if v := lowest.Unpack(c.word); v != c.v {
			t.Errorf("Min. Cos phi: Unpack(%d) = %g, want %g", c.word, v, c.v)
		}
	}
}
