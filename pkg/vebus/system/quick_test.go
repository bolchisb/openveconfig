package system

import (
	"slices"
	"strings"
	"testing"
)

// quickExamples maps each Quick Configure layout to its worked example
// records (#53 §4.11, §9.2), in role order, built for sending.
var quickExamples = map[QuickLayout][]string{
	QuickStandalone:   {"01 01 01 00 00 00 00 F0 00"},
	QuickParallel2:    {"02 02 09 00 00 00 00 F0 00", "FF FF 09 00 00 00 00 F0 03"},
	QuickParallel3:    {"03 03 49 00 00 00 00 F0 00", "FF FF 49 00 00 00 00 F0 03", "FF FF 49 00 00 00 00 F0 06"},
	QuickSplit120:     {"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 04 F5 01"},
	QuickSplit180:     {"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 06 F5 01"},
	QuickSplit240:     {"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 08 F5 01"},
	QuickSplit180Auto: {"01 01 03 00 00 00 00 F4 00", "01 01 03 00 00 00 C6 F5 01"},
	QuickThreePhase:   {"01 01 07 00 00 00 00 F8 00", "01 01 07 00 00 00 04 F9 01", "01 01 07 00 00 00 08 FA 02"},
}

// stored returns the stored form of a record built for sending on a unit
// with assistants: bit 4 clear (E for F in byte 7's high nibble).
func stored(r Record) Record {
	r.SetAssistantResetFlag(false)
	return r
}

func TestQuickTemplates(t *testing.T) {
	if len(quickExamples) != len(QuickLayouts) {
		t.Fatalf("%d examples for %d layouts", len(quickExamples), len(QuickLayouts))
	}
	for _, q := range QuickLayouts {
		t.Run(q.String(), func(t *testing.T) {
			want := quickExamples[q]
			if q.Units() != len(want) {
				t.Fatalf("Units() = %d, want %d", q.Units(), len(want))
			}
			us := multis(q.Units())
			recs, err := q.Records(us, true)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := q.Records(us, false)
			if err != nil {
				t.Fatal(err)
			}
			for i, r := range recs {
				w := hexRecord(t, want[i])
				if r.Unit != us[i] {
					t.Errorf("record %d is for unit %s, want role order", i, r.Unit.Label())
				}
				if r.Record != w {
					t.Errorf("role %d: %s, want %s", i, r.Record.Hex(), w.Hex())
				}
				if preview[i].Record != stored(w) {
					t.Errorf("role %d preview: %s, want %s", i, preview[i].Record.Hex(), stored(w).Hex())
				}
				if encode(r.Record) != r.Record {
					t.Errorf("round trip of %s gives %s", r.Record.Hex(), encode(r.Record).Hex())
				}
			}
			// layout → records → layout gives the same layout.
			l, err := q.Layout(us)
			if err != nil {
				t.Fatal(err)
			}
			apply(recs)
			read := slices.Clone(us)
			slices.Reverse(read)
			back, is := FromRecords(read)
			if is != nil {
				t.Fatal(is.Detail)
			}
			assertSameLayout(t, back, l)
			if again := back.Records(true); !slices.Equal(again, l.Records(true)) {
				t.Errorf("records after read-back differ")
			}
			if issues := l.Check(); len(issues) != 0 {
				t.Errorf("issues %v", issues)
			}
		})
	}
}

// TestQuickDualInput: Quick Configure sets byte 7 bit 5 also on dual-input
// units, whose second input is unused with one AC input.
func TestQuickDualInput(t *testing.T) {
	us := []*Unit{dual(1), dual(2)}
	recs, err := QuickParallel2.Records(us, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range recs {
		if w := hexRecord(t, quickExamples[QuickParallel2][i]); r.Record != w {
			t.Errorf("role %d: %s, want %s", i, r.Record.Hex(), w.Hex())
		}
	}
	l, _ := QuickParallel2.Layout(us)
	if got := recordOf(t, l.Records(true), us[0]); got.SecondInputGrouped() {
		t.Errorf("System Configurator rule gives bit 5 set: %s", got.Hex())
	}
}

func TestQuickLayoutErrors(t *testing.T) {
	if _, err := QuickParallel3.Layout(multis(2)); err == nil {
		t.Error("parallel3 accepted 2 units")
	}
	if _, err := QuickLayout(99).Layout(multis(1)); err == nil {
		t.Error("unknown layout accepted")
	}
	u := multi(1)
	if _, err := QuickParallel2.Layout([]*Unit{u, u}); err == nil {
		t.Error("the same unit accepted twice")
	}
}

func TestStandalone(t *testing.T) {
	for _, tc := range []struct {
		u    *Unit
		send bool
		want string
	}{
		{multi(1), true, "01 01 01 00 00 00 00 F0 00"},
		{multi(1), false, "01 01 01 00 00 00 00 E0 00"},
		{withFW(multi(1), 2623150), true, "01 01 01 00 00 00 00 E0 00"},
		{dual(1), true, "01 01 01 00 00 00 00 F0 00"},
		{inverter(1), true, "01 01 01 00 00 00 00 F0 00"},
	} {
		if got := Standalone(tc.u, tc.send); got != hexRecord(t, tc.want) {
			t.Errorf("Standalone(%02X, rev %d, %v) = %s, want %s", tc.u.ResponseChar, tc.u.Revision(), tc.send, got.Hex(), tc.want)
		}
	}
	// It equals the factory record, with bit 4 per the rule.
	if Standalone(withFW(multi(1), 2623150), true) != factoryRecord {
		t.Error("standalone record differs from the factory record")
	}
}

func TestRecognizeQuick(t *testing.T) {
	for _, q := range QuickLayouts {
		t.Run(q.String(), func(t *testing.T) {
			us := multis(q.Units())
			recs, _ := q.Records(us, true)
			apply(recs)
			// Units with assistants hold the records with bit 4 clear.
			us[0].Record = stored(us[0].Record)
			read := slices.Clone(us)
			slices.Reverse(read)
			got, ordered, ok := RecognizeQuick(read)
			if !ok || got != q || !slices.Equal(ordered, us) {
				t.Errorf("RecognizeQuick = %s %v %v, want %s in role order", got, ordered, ok, q)
			}
		})
	}
	noMatch := map[string]func() []*Unit{
		"System Configurator layout": func() []*Unit { return built(t, "E11") },
		"twelve in parallel":         func() []*Unit { return built(t, "E8") },
		"factory units":              func() []*Unit { u := multis(2); u[0].Record = factoryRecord; u[1].Record = factoryRecord; return u },
		"no units":                   func() []*Unit { return nil },
		"group off": func() []*Unit {
			us := built(t, "E2")
			for _, u := range us {
				u.Record.SetGrouped(false)
			}
			return us
		},
	}
	for name, f := range noMatch {
		if q, _, ok := RecognizeQuick(f()); ok {
			t.Errorf("%s recognised as %s", name, q)
		}
	}
}

func TestCheckQuickUnit(t *testing.T) {
	first := multi(1)
	letterA := multi(2)
	letterA.Letter = 0
	split := withFW(multi(3), 2060209)
	for _, tc := range []struct {
		name  string
		q     QuickLayout
		first *Unit
		u     *Unit
		want  []Rule
		msg   string
	}{
		{"same unit type", QuickParallel3, first, multi(2), nil, ""},
		{"first unit against itself", QuickThreePhase, first, first, nil, ""},
		{"firmware differs", QuickParallel2, first, withFW(multi(2), 2623457), []Rule{RuleQuickFirmware}, "same type and firmware version"},
		{"letter differs in parallel", QuickParallel2, first, letterA, []Rule{RuleQuickLetter}, "letter (A) differs from the first unit's (B)"},
		{"letter differs in three phase", QuickThreePhase, first, letterA, nil, ""},
		{"split-phase model in split phase", QuickSplit180, split, split, []Rule{RuleQuickSplitModel}, "120/240 V split-phase models"},
		{"split-phase model in parallel", QuickParallel2, split, withFW(multi(4), 2060209), nil, ""},
		{"split-phase model, revision 210", QuickThreePhase, withFW(multi(3), 2060210), withFW(multi(4), 2060210), nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues := CheckQuickUnit(tc.q, tc.first, tc.u)
			if got := rules(issues); !slices.Equal(got, tc.want) {
				t.Fatalf("rules %v, want %v", got, tc.want)
			}
			for _, is := range issues {
				if is.Severity != Error || !strings.Contains(is.Message, tc.msg) {
					t.Errorf("issue %+v", is)
				}
			}
		})
	}
}
