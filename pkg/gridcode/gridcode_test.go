package gridcode

import (
	"fmt"
	"math"
	"math/bits"
	"slices"
	"testing"
)

// put stores field f under p's mask in word, as the device would hold it.
func put(p Param, word uint16, f int) uint16 {
	return word&^p.Mask | uint16(f)<<p.shift()&p.Mask
}

// fieldValues returns the field values a round trip covers for p.
func fieldValues(p Param) []int {
	switch p.Kind {
	case Checkbox:
		return []int{0, int(p.Mask >> p.shift())}
	case Choice:
		var fs []int
		for f := range p.Choices {
			fs = append(fs, f)
		}
		return fs
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
		for _, compact := range []bool{false, true} {
			ps, _ := ReactiveParams(compact, 0.9)
			q := *p
			q.Name += fmt.Sprintf(" with reactive power (compact %t)", compact)
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
					if err != nil || got&^p.Mask != ^base&^p.Mask || p.Field(got) != f {
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
		{pageGermanyB, "Voltage threshold to enable the curve (lock-in)", 70, 0x8800}, // -120
		{pageGermanyB, "Reactive-power response time constant", 3.0002, 4581},
		{pageAustria, "P(U) curve: voltage span to zero power", 4.6, 18236},
		{pageAUNZM, "P(U) point 1: charge power limit", 20, 148 << 8},
		{pageMicrogrid, "Active power at reference frequency (P0)", -100, 1024},
		{pageMicrogrid, "Fixed black-start retry delay", 0, 0xFF00},
		{pageAUNZ2015, "Demand-response mode 0 enabled", 1, 1},
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
				if p.Mask != 0xFFFF || p.High < 129 || p.High > 190 || p.High == p.Word {
					t.Errorf("%s: invalid two-word field (high word %d)", name, p.High)
				}
				w = 32
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
				if bits.OnesCount16(p.Mask) != 1 {
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
// word, apart from the sharing the spec describes: Germany B's
// reactive-power sub-pages reuse words 150-157 and its constant Q shares
// the word of Q(U) point 1; the microgrid diagnostic points reuse the
// words of the page's main parameters.
func TestNoOverlap(t *testing.T) {
	type pair struct{ a, b string }
	allowed := map[*Page][]pair{
		pageGermanyB: {
			{"Reactive power: cos phi as a function of active power", "Reactive power: Q as a function of voltage"},
			{"Reactive power: cos phi as a function of active power", "Reactive power: limits for the Q set-point"},
			{"Reactive power: Q as a function of voltage", "Reactive power: limits for the Q set-point"},
			{"Q(U) point 1: reactive power", "Constant reactive power (fixed mode)"},
		},
	}
	ok := func(pg *Page, p, q Param) bool {
		for _, a := range allowed[pg] {
			if p.Group != q.Group && (a == pair{p.Group, q.Group} || a == pair{q.Group, p.Group}) {
				return true
			}
			if p.Group == q.Group && (a == pair{p.Name, q.Name} || a == pair{q.Name, p.Name}) {
				return true
			}
		}
		return false
	}
	for _, pg := range testPages() {
		for i, p := range pg.Params {
			for _, q := range pg.Params[i+1:] {
				// Never shown together: they follow different values of a field.
				apart := p.ShowMask != 0 && p.ShowWord == q.ShowWord && p.ShowMask == q.ShowMask && p.ShowValue != q.ShowValue
				if p.Word == q.Word && p.Mask&q.Mask != 0 && !apart && !ok(pg, p, q) {
					t.Errorf("%s: %q and %q share bits %#04x of word %d", pg.Name, p.Name, q.Name, p.Mask&q.Mask, p.Word)
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
	// Not known: which Germany variant 4 and 5 use, and the pages of 18,
	// 19, 20, 30, 31; codes outside the list.
	for _, c := range []Code{4, 5, 18, 19, 20, 30, 31, 33, 0xFFFF} {
		if p, known := PageFor(c); known || p != nil {
			t.Errorf("PageFor(%d) = %v, %v; want unknown", c, p, known)
		}
	}
	for _, c := range []Code{0, 1, 21} {
		if p, known := PageFor(c); !known || p != nil {
			t.Errorf("PageFor(%d) = %v, %v; want no page", c, p, known)
		}
	}
	for c, want := range map[Code]*Page{
		2: pageUK, 3: pageItaly, 6: pageAUNZ2015, 16: pageAUNZ2015, 10: pageSpain,
		17: pageEurope, 22: pageAUNZ2020, 29: pageAUNZM, 32: pagePoland,
	} {
		if p, known := PageFor(c); !known || p != want {
			t.Errorf("PageFor(%d) = %v, %v; want %s", c, p, known, want.Name)
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
	const limit = "Active-power threshold to enable the curve (lock-in)"
	if p := pageRomania.WithReactivePower(14, -1); !has(p, "Reactive-power control mode") || has(p, limit) || has(pageRomania, "Reactive-power control mode") {
		t.Error("Romania: want layout A on a copy of the page")
	}
	if p := pageEurope.WithReactivePower(17, -1); !has(p, limit) {
		t.Error("Europe: want layout B with the limitation box")
	}
	if p := pageFrance.WithReactivePower(9, -1); has(p, "Reactive-power control mode") || !slices.Contains(p.Unsupported, ReactivePowerUnknownLayout) {
		t.Error("France without a version: want the group listed as not supported")
	}
	if p := pageAUNZ2015.WithReactivePower(6, 0); p != pageAUNZ2015 {
		t.Error("AS/NZS 4777 (2015) has no reactive-power group")
	}
	ps, _ := ReactiveParams(false, lowestCosPhi(14, -1))
	param := func(name string) Param {
		return ps[slices.IndexFunc(ps, func(q Param) bool { return q.Name == name })]
	}
	if v := param("Reactive-power response time constant").Unpack(4165); math.Abs(v-3.3) > 0.001 {
		t.Errorf("filter time %g, want 3.3", v)
	}
	if v := param("cos-phi(P) point 2: active power").Unpack(9830); math.Abs(v-30) > 0.01 {
		t.Errorf("cos phi(P) point 2 at %g %%, want 30", v)
	}
	if p := param("cos-phi(P) point 2: active power"); p.Shown(0) || !p.Shown(1) || !p.Shown(0xFFF5) {
		t.Error("cos phi(P) points not shown with regulation 1 only")
	}
	// The cos phi conversion of the original: 32768 is 1.00 (the Romania
	// file), above it a positive cos phi, below it a negative one, and a
	// cos phi nearer to 0 than the code's lowest is written as the lowest.
	fixed := param("Constant cos phi (fixed mode)")
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
}
