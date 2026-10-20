package gridcode

import (
	"errors"
	"math"
	"slices"
	"testing"
)

// ceiParam returns the parameter of the Italy page named name.
func ceiParam(t *testing.T, name string) Param {
	t.Helper()
	i := slices.IndexFunc(pageItaly.Params, func(p Param) bool { return p.Name == name })
	if i < 0 {
		t.Fatalf("no parameter %q", name)
	}
	return pageItaly.Params[i]
}

// TestCEICurve checks the CEI 0-21 curves with made-up frequencies and
// droops.
func TestCEICurve(t *testing.T) {
	const (
		upStart   = "Network-support mode: upper activation frequency"
		lowStart  = "Network-support mode: lower activation frequency"
		upStop    = "Network-support mode: release band, upper frequency"
		lowStop   = "Network-support mode: release band, lower frequency"
		upDroop   = "Droop paired with power limit PCMax"
		lowDroop  = "Droop paired with power limit PSMax"
		stopDelay = "Network-support mode: time inside release band"
	)
	words := map[uint16]uint16{}
	word := func(w uint16) uint16 { return words[w] }
	set := func(name string, v float64) error {
		t.Helper()
		ch, err := ceiParam(t, name).PackIn(word, v)
		for w, x := range ch {
			words[w] = x
		}
		return err
	}
	shown := func(name string) float64 {
		p := ceiParam(t, name)
		return p.Value(p.FieldIn(word))
	}
	for _, s := range []struct {
		name string
		v    float64
	}{{lowStop, 49.85}, {upStop, 50.15}, {upStart, 50.45}, {lowStart, 49.55}} {
		p := ceiParam(t, s.name)
		words[p.Word], _ = p.Pack(0, s.v)
	}
	for _, s := range []struct {
		name string
		v    float64
	}{{upDroop, 3.1}, {lowDroop, 1.7}, {upStart, 50.45}, {lowStart, 49.55}} {
		if err := set(s.name, s.v); err != nil {
			t.Fatalf("%s = %g: %v", s.name, s.v, err)
		}
	}
	for _, s := range []struct {
		name string
		want float64
	}{{upStart, 50.45}, {lowStart, 49.55}, {upStop, 50.15}, {lowStop, 49.85}, {upDroop, 3.1}, {lowDroop, 1.7}} {
		if got := shown(s.name); math.Abs(got-s.want) > 0.001 {
			t.Errorf("%s shows %g, want %g", s.name, got, s.want)
		}
	}
	if w := words[ceiOverStartWord]; w != 49554 { // 2 500 000 / 50.45
		t.Errorf("upper start word %d, want 49554", w)
	}
	// Each curve falls from 65536 at its low frequency to 0 at its high
	// one: the two pieces, a slope per period tick (times 256) and the
	// value at their end, follow it to within 0.5 % of the full scale.
	for _, c := range []struct {
		first   uint16
		lo, hi  float64
		descr   string
		falling bool
	}{
		{ceiOverStartWord, 50.45, 50.45 + 3.1/2, "upper", true},
		{ceiUnderStartWord, 49.55 - 1.7/2, 49.55, "lower", true},
	} {
		curve := func(f float64) float64 { return 65536 * (c.hi - f) / (c.hi - c.lo) }
		half := float64(words[c.first+1])
		ps := ceiTicks / c.lo
		for i, piece := range []struct{ start, end float64 }{{ps, ps - half}, {ps - half, ps - 2*half}} {
			slope, end := float64(words[c.first+2+2*uint16(i)])/256, float64(words[c.first+3+2*uint16(i)])
			atStart := end + slope*half
			if d := math.Abs(atStart - curve(ceiTicks/piece.start)); d > 330 {
				t.Errorf("%s curve, piece %d: %g at its start, want %g", c.descr, i+1, atStart, curve(ceiTicks/piece.start))
			}
			if d := math.Abs(end - curve(ceiTicks/piece.end)); d > 330 {
				t.Errorf("%s curve, piece %d: %g at its end, want %g", c.descr, i+1, end, curve(ceiTicks/piece.end))
			}
		}
	}
	// A new start frequency keeps the droop; a new droop keeps the start.
	if err := set(upStart, 50.6); err != nil || math.Abs(shown(upDroop)-3.1) > 0.001 {
		t.Errorf("after a new upper start: %v, droop %g", err, shown(upDroop))
	}
	if err := set(lowDroop, 2.5); err != nil || math.Abs(shown(lowStart)-49.55) > 0.001 {
		t.Errorf("after a new lower droop: %v, start %g", err, shown(lowStart))
	}
	// Out of order: refused, nothing changes.
	before := cloneWords(words)
	for _, s := range []struct {
		name string
		v    float64
	}{{upStop, 50.7}, {lowStop, 49.5}, {upStart, 50.15}, {lowStart, 49.9}} {
		if ch, err := ceiParam(t, s.name).PackIn(word, s.v); !errors.Is(err, ErrFrequencyOrder) || ch != nil {
			t.Errorf("%s = %g: %v, %v; want ErrFrequencyOrder", s.name, s.v, ch, err)
		}
	}
	if !equalWords(before, words) {
		t.Error("a refused value changed the words")
	}
	// The droop is not a single-word field.
	if _, err := ceiParam(t, upDroop).Pack(0, 2); err == nil {
		t.Error("Pack of a droop: want an error")
	}
	// The delays count ticks of 26.2144 ms from 2.
	if v := ceiParam(t, stopDelay).Unpack(40); math.Abs(v-38*0.0262144) > 1e-9 {
		t.Errorf("stop delay of field 40: %g s", v)
	}
	if v := ceiParam(t, "Network-support mode: activation delay").Unpack(2); v != 0 {
		t.Errorf("activation delay of field 2: %g s, want 0", v)
	}
}

func cloneWords(m map[uint16]uint16) map[uint16]uint16 {
	c := map[uint16]uint16{}
	for k, v := range m {
		c[k] = v
	}
	return c
}

func equalWords(a, b map[uint16]uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestForCode checks the captions, tables and the Germany droop that
// follow the grid code and the grid-info table.
func TestForCode(t *testing.T) {
	none, device := TableModel(nil), Model{}
	g99 := TableModel(withEntry(maskTable(), 31, 2)) // code 2 with a mask for word 148, code 31 listed
	hasDroop := func(p *Page) bool {
		return slices.ContainsFunc(p.Params, func(prm Param) bool { return prm.Name == germanyDroop })
	}
	for _, tc := range []struct {
		c     Code
		m     Model
		title string
		table [][]string
	}{
		{2, g99, "UK G99 grid code settings", tableG99},
		{31, g99, "UK G98 grid code settings", tableG99},
		{2, none, "", tableG59},
		{31, none, "UK G98 grid code settings", tableG59},
		{2, device, "", nil},
		{20, device, "Northern Ireland grid code settings", tableNorthernIreland},
		{5, none, "", tableVDE(false)},
		{5, device, "", nil},
		{4, none, "", nil},
	} {
		p, _ := PageOf(tc.c, tc.m)
		if p.Title != tc.title || !slices.EqualFunc(p.Table, tc.table, slices.Equal) {
			t.Errorf("code %d: title %q, table %q; want %q, %q", tc.c, p.Title, p.Table, tc.title, tc.table)
		}
	}
	if tableVDE(true)[1][2] != "> 3s" {
		t.Error("VDE table of the 2018 edition: want the under-voltage delay > 3s")
	}
	for _, c := range []Code{4, 5} {
		if p, _ := PageOf(c, none); hasDroop(p) {
			t.Errorf("code %d without a mask for word 158: want no droop", c)
		}
		if p, _ := PageOf(c, device); !hasDroop(p) || !hasDroop(pageGermanyA) {
			t.Errorf("code %d with an unknown table: want the droop", c)
		}
	}
}

func TestInternalProtection(t *testing.T) {
	for _, tc := range []struct {
		extra []byte
		want  bool
	}{{nil, false}, {[]byte{0xFF}, false}, {[]byte{0, 0x04}, true}, {[]byte{0xFF, 0xFB, 0xFF}, false}} {
		if got := InternalProtection(tc.extra); got != tc.want {
			t.Errorf("InternalProtection(% X) = %t, want %t", tc.extra, got, tc.want)
		}
	}
}
