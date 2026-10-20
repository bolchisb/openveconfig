package system

import (
	"math/rand/v2"
	"slices"
	"testing"
)

type workedExample struct {
	name  string
	build func(t *testing.T) (*Layout, []*Unit)
	want  []string // record of each returned unit, built for sending
}

// workedExamples are the worked examples of the spec (#53 §4.11).
func workedExamples() []workedExample {
	parallel := func(n int) func(t *testing.T) (*Layout, []*Unit) {
		return func(t *testing.T) (*Layout, []*Unit) {
			us := multis(n)
			return newLayout(t, SinglePhase, 1, Fixed, inCell(cell(0, 0), us...)...), us
		}
	}
	phases := func(s Setup, opt L2Option, perPhase int) func(t *testing.T) (*Layout, []*Unit) {
		return func(t *testing.T) (*Layout, []*Unit) {
			us := multis(s.Phases() * perPhase)
			var ps []placement
			for i, u := range us {
				ps = append(ps, placement{u, cell(i/perPhase, 0)})
			}
			return newLayout(t, s, 1, opt, ps...), us
		}
	}
	twoInputs := func(extBF bool) func(t *testing.T) (*Layout, []*Unit) {
		return func(t *testing.T) (*Layout, []*Unit) {
			us := multis(2)
			for _, u := range us {
				u.ExternalBF = extBF
			}
			return newLayout(t, SinglePhase, 2, Fixed, placement{us[0], cell(0, 0)}, placement{us[1], cell(0, 1)}), us
		}
	}
	twelve := []string{"0C 0C 4B 92 24 49 00 F0 00"}
	for _, a := range []string{"01", "03", "06", "09", "0C", "0F", "12", "15", "18", "1B", "1E"} {
		twelve = append(twelve, "FF FF 4B 92 24 49 00 F0 "+a)
	}
	return []workedExample{
		{"E1 standalone", parallel(1), []string{"01 01 01 00 00 00 00 F0 00"}},
		{"E1b standalone revision 150", func(t *testing.T) (*Layout, []*Unit) {
			u := multi(1)
			u.VersionWord = 2623150
			return newLayout(t, SinglePhase, 1, Fixed, placement{u, cell(0, 0)}), []*Unit{u}
		}, []string{"01 01 01 00 00 00 00 E0 00"}},
		{"E2 two parallel", parallel(2), []string{"02 02 09 00 00 00 00 F0 00", "FF FF 09 00 00 00 00 F0 03"}},
		{"E3 three parallel", parallel(3), []string{"03 03 49 00 00 00 00 F0 00", "FF FF 49 00 00 00 00 F0 03", "FF FF 49 00 00 00 00 F0 06"}},
		{"E4 dual 120 fixed", phases(Dual120, Fixed, 1), []string{"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 04 F5 01"}},
		{"E5 dual 180 fixed", phases(Dual180, Fixed, 1), []string{"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 06 F5 01"}},
		{"E5 dual 180 floating", phases(Dual180, Floating, 1), []string{"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 86 F5 01"}},
		{"E5 dual 180 floating with return", phases(Dual180, FloatingReturn, 1), []string{"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 C6 F5 01"}},
		{"E6 three phase", phases(ThreePhase, Fixed, 1), []string{"01 01 07 00 00 00 00 F8 00", "01 01 07 00 00 00 04 F9 01", "01 01 07 00 00 00 08 FA 02"}},
		{"E7 three phase two per phase", phases(ThreePhase, Fixed, 2), []string{
			"02 02 3F 00 00 00 00 F8 00", "FF FF 3F 00 00 00 00 F8 03",
			"02 02 3F 00 00 00 04 F9 01", "FF FF 3F 00 00 00 04 F9 04",
			"02 02 3F 00 00 00 08 FA 02", "FF FF 3F 00 00 00 08 FA 05",
		}},
		{"E8 twelve units in L1", parallel(12), twelve},
		{"E9 dual-input unit, second input unused", func(t *testing.T) (*Layout, []*Unit) {
			u := dual(1)
			return newLayout(t, SinglePhase, 1, Fixed, placement{u, cell(0, 0)}), []*Unit{u}
		}, []string{"01 01 01 00 00 00 00 D0 00"}},
		{"E10 dual-input unit with second input before two Multis", func(t *testing.T) (*Layout, []*Unit) {
			d, m1, m2 := dual(1), multi(2), multi(3)
			l := newLayout(t, SinglePhase, 2, Fixed, placement{d, cell(0, 0)}, placement{m1, cell(0, 1)}, placement{m2, cell(0, 1)})
			return l, []*Unit{d, m1, m2}
		}, []string{"03 03 49 00 00 00 00 F0 00", "FF FF 49 00 00 00 10 F0 03", "FF FF 49 00 00 00 10 F0 06"}},
		{"E11 two AC inputs", twoInputs(false), []string{"02 02 09 00 00 00 00 F0 00", "02 02 09 00 00 00 10 F0 03"}},
		{"E12 two AC inputs, external BF", twoInputs(true), []string{"02 02 09 00 00 00 00 F0 00", "FF FF 09 00 00 00 10 F0 03"}},
	}
}

func TestWorkedExamples(t *testing.T) {
	for _, ex := range workedExamples() {
		t.Run(ex.name, func(t *testing.T) {
			l, us := ex.build(t)
			recs := l.Records(true)
			if len(recs) != len(ex.want) {
				t.Fatalf("got %d records, want %d", len(recs), len(ex.want))
			}
			for i, u := range us {
				if got, want := recordOf(t, recs, u), hexRecord(t, ex.want[i]); got != want {
					t.Errorf("unit %d: got %s, want %s", i, got.Hex(), want.Hex())
				}
			}
		})
	}
}

// TestExternalBFByVersion: a version word with high byte 0xC7 counts as
// external BF for master marking.
func TestExternalBFByVersion(t *testing.T) {
	us := multis(2)
	us[1].VersionWord |= 0xC7 << 24
	l := newLayout(t, SinglePhase, 2, Fixed, placement{us[0], cell(0, 0)}, placement{us[1], cell(0, 1)})
	if got := recordOf(t, l.Records(true), us[1]); got.Leader() {
		t.Errorf("unit with version high byte 0xC7 on AC input 2 is marked: %s", got.Hex())
	}
}

// TestPreviewKeepsAssistantFlag: building for preview keeps bit 4 where the
// flag applies, sending sets it, and it is clear where it does not apply.
func TestPreviewKeepsAssistantFlag(t *testing.T) {
	withAssistants, bare, old := multi(1), multi(2), multi(3)
	bare.Record.SetAssistantResetFlag(true)
	old.VersionWord = 2623204
	old.Record.SetAssistantResetFlag(true)
	l := newLayout(t, SinglePhase, 1, Fixed, inCell(cell(0, 0), withAssistants, bare, old)...)
	for _, tc := range []struct {
		send bool
		u    *Unit
		want bool
	}{
		{false, withAssistants, false},
		{false, bare, true},
		{false, old, false},
		{true, withAssistants, true},
		{true, bare, true},
		{true, old, false},
	} {
		if got := recordOf(t, l.Records(tc.send), tc.u).AssistantResetFlag(); got != tc.want {
			t.Errorf("send %v, unit %s: flag %v, want %v", tc.send, tc.u.Label(), got, tc.want)
		}
	}
}

// TestPositionZeroSecondInput: a cell whose position 0 is a second input
// has no leader.
func TestPositionZeroSecondInput(t *testing.T) {
	d, m := dual(1), multi(2)
	l := newLayout(t, SinglePhase, 2, Fixed, placement{d, cell(0, 0)}, placement{m, cell(0, 1)})
	if r := recordOf(t, l.Records(true), m); r.Leader() {
		t.Errorf("unit after a second input is marked: %s", r.Hex())
	}
}

func TestAllocate(t *testing.T) {
	seq := func(from, n int) []byte {
		var s []byte
		for i := range n {
			s = append(s, byte(from+3*i))
		}
		return s
	}
	rep := func(p, n int) []int { return slices.Repeat([]int{p}, n) }
	cat := func(ps ...[]int) []int { return slices.Concat(ps...) }
	for _, tc := range []struct {
		name   string
		phases []int
		want   []byte
		mask   uint32
	}{
		{"empty", nil, []byte{}, 0},
		{"one per phase", []int{0, 1, 2}, []byte{0, 1, 2}, 0x07},
		{"full phases 11/11/10", cat(rep(0, 11), rep(1, 11), rep(2, 10)),
			slices.Concat(seq(0, 11), seq(1, 11), seq(2, 10)), 0xFFFFFFFF},
		{"interleaved order", []int{2, 0, 1, 0}, []byte{2, 0, 1, 3}, 0x0F},
		// 12th L1 unit borrows from L2 (11 free vs 10); 1 is inserted in
		// sorted order, so the second unit gets it (spec correction).
		{"L1 borrows from L2", rep(0, 12), append([]byte{0, 1}, seq(3, 10)...), 0x4924924B},
		// L2 has one unit (counter 1, 10 free) and L3 none (10 free): the
		// tie goes to the lower phase, L2, which gives 4.
		{"tie goes to lower phase", cat(rep(0, 12), rep(1, 1)),
			append(append([]byte{0, 3, 4}, seq(6, 9)...), 1), 0x4924924B | 1<<1 | 1<<4},
		// L2 has three units (8 free), L3 none (10 free): L3 gives 2.
		{"donor with most free", cat(rep(0, 12), rep(1, 3)),
			append(append([]byte{0, 2}, seq(3, 10)...), 1, 4, 7), 0x49249249 | 1<<2 | 1<<1 | 1<<4 | 1<<7},
		// L2 overflows and borrows from L1 (11 free vs L3 10): 0.
		{"L2 borrows from L1", rep(1, 12), append([]byte{0}, seq(1, 11)...), 0x92492493},
		// 23 units in L1: L2 gives all 11, then L3 gives 2.
		{"third phase donates", rep(0, 23),
			[]byte{0, 1, 2, 3, 4, 6, 7, 9, 10, 12, 13, 15, 16, 18, 19, 21, 22, 24, 25, 27, 28, 30, 31}, 0xDB6DB6DF},
		// L1 and L2 both overflow; L2 cannot donate to L1, so L3 gives 2 to
		// L1 and then 5 to L2.
		{"overflowed phase does not donate", cat(rep(0, 12), rep(1, 12)),
			slices.Concat([]byte{0, 2}, seq(3, 10), []byte{1, 4, 5}, seq(7, 9)), 0xFFFFFFFF &^ (1<<8 | 1<<11 | 1<<14 | 1<<17 | 1<<20 | 1<<23 | 1<<26 | 1<<29)},
		// 33 units: 11 + 11 + 10 addresses, the last unit gets none.
		{"more than 32 units", rep(0, 33), append(slices.Sorted(slices.Values(slices.Concat(seq(0, 11), seq(1, 11), seq(2, 10)))), NoAddress), 0xFFFFFFFF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, mask, counts := allocate(tc.phases)
			if !slices.Equal(got, tc.want) {
				t.Errorf("addresses\n got %v\nwant %v", got, tc.want)
			}
			if mask != tc.mask {
				t.Errorf("mask %08X, want %08X", mask, tc.mask)
			}
			var want [maxPhases]int
			for _, p := range tc.phases {
				want[p]++
			}
			if counts != want {
				t.Errorf("counts %v, want %v", counts, want)
			}
			seen := map[byte]bool{}
			for _, a := range got {
				if a != NoAddress && seen[a] {
					t.Errorf("address %d given twice", a)
				}
				seen[a] = true
			}
		})
	}
}

// TestAllocateFuzz: for any assignment of up to 32 units the addresses are
// distinct, all below 32, and the mask has one bit per unit.
func TestAllocateFuzz(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		phases := make([]int, r.IntN(33))
		for i := range phases {
			phases[i] = r.IntN(3)
		}
		addrs, mask, _ := allocate(phases)
		seen := uint32(0)
		for _, a := range addrs {
			if a > 31 || seen&(1<<a) != 0 {
				t.Fatalf("phases %v: addresses %v", phases, addrs)
			}
			seen |= 1 << a
		}
		if seen != mask {
			t.Fatalf("phases %v: mask %08X, addresses give %08X", phases, mask, seen)
		}
	}
}

// TestRoundTripExamples: records → layout → records gives the same bytes and
// the same layout for every worked example, whatever the order the units
// are read in.
func TestRoundTripExamples(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for _, ex := range workedExamples() {
		t.Run(ex.name, func(t *testing.T) {
			l, us := ex.build(t)
			recs := l.Records(true)
			apply(recs)
			read := slices.Clone(us)
			r.Shuffle(len(read), func(i, j int) { read[i], read[j] = read[j], read[i] })
			got, issue := FromRecords(read)
			if issue != nil {
				t.Fatalf("inconsistent: %s (%s)", issue.Message, issue.Detail)
			}
			assertSameLayout(t, got, l)
			if d := got.Differing(); len(d) != 0 {
				t.Errorf("units differ from their records: %v", d)
			}
			again := got.Records(true)
			if !slices.Equal(again, recs) {
				t.Errorf("rebuilt records differ:\n got %v\nwant %v", again, recs)
			}
		})
	}
}

func assertSameLayout(t *testing.T, got, want *Layout) {
	t.Helper()
	if got.Setup != want.Setup || got.ACInputs != want.ACInputs || got.L2 != want.L2 {
		t.Errorf("choices %v/%d/%v, want %v/%d/%v", got.Setup, got.ACInputs, got.L2, want.Setup, want.ACInputs, want.L2)
	}
	for a := range want.ACInputs {
		if got.Grouped[a] != want.Grouped[a] {
			t.Errorf("AC input %d grouped %v, want %v", a+1, got.Grouped[a], want.Grouped[a])
		}
	}
	want.each(func(c Cell, list []Entry) {
		if g := got.Entries(c); !slices.Equal(g, list) {
			t.Errorf("cell %s: got %v, want %v", c, g, list)
		}
	})
}
