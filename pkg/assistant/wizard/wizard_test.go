package wizard

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

// synthSource is a made-up assistant: a radio step, an information step,
// a step that only some choices reach, two voltages read back from the
// code they compile to, and a confirmation.
const synthSource = `
Bat(v) .EQU 'hwDC*v/12'
Battery .AID Battery, RL, 3, 1
Capacity .AID Capacity, EL, '0N 10 9999 200'
Volts .AID Volts, E, '2B '+str(Bat(1000))+' '+str(Bat(1500))+' '+str(Bat(1250)), '2B 4000 6000 4800'
Done .AID Done, C, 2, 0
Note .AID Note, E
Oops .AID Oops, E
 .AUE Battery, btype
 .AIP Note, btype*10
 .IF btype = 2
  .AUE Capacity, cap
 .ELSE
cap .SET 0
 .ENDIF
 .AUE Volts, v1, v2
v1 .ALD RB2($+1)
 DATA 0x44, v1, v1 >> 8
v2 .ALD RBO($)
 DATA 0x44, v2, v2 >> 8
 DATA btype, cap
 .AUE Done, ok
 .IF ok = 0
  .AER Oops
 .ENDIF
 DATA len(AssistantText('Battery')), AssistantLabelValue('btype')
`

func synthProgram() *Program {
	return &Program{
		Main:   "synth",
		Source: []byte(synthSource),
		Labels: map[string]asm.Value{"hwDC": asm.IntVal(48)},
		Texts:  map[string]Text{"synth": ParseText([]byte("#Battery\r\nPick one\r\n#Other\r\nx\r\n"))},
	}
}

func ints(v ...int64) []asm.Value {
	out := make([]asm.Value, len(v))
	for i, n := range v {
		out[i] = asm.IntVal(n)
	}
	return out
}

func vals(st *Step) []int64 {
	var out []int64
	for _, v := range st.Values {
		out = append(out, v.I)
	}
	return out
}

func wantStep(t *testing.T, st *Step, err error, name string, values ...int64) {
	t.Helper()
	if err != nil {
		t.Fatalf("at %s: %v", name, err)
	}
	if st == nil || st.Name != name {
		t.Fatalf("step = %+v, want %s", st, name)
	}
	if got := vals(st); len(got) != len(values) || len(values) > 0 && !equalInts(got, values) {
		t.Errorf("%s values = %v, want %v", name, got, values)
	}
}

func equalInts(a, b []int64) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return len(a) == len(b)
}

func TestSession(t *testing.T) {
	p := synthProgram()
	s := p.NewSession(nil)
	st, err := s.Start()
	wantStep(t, st, err, "Battery", 1)
	if st.Type != 'R' || !st.Listed || st.Buttons != 3 || st.Text != "Pick one" {
		t.Errorf("battery step %+v", st)
	}
	if _, err := s.Next(ints(3)); err == nil {
		t.Error("button 3 of 3 accepted")
	}
	st, err = s.Next(ints(2))
	wantStep(t, st, err, "Note", 20)
	st, err = s.Next(nil)
	wantStep(t, st, err, "Capacity", 200)
	st, err = s.Next(ints(150))
	wantStep(t, st, err, "Volts", 5000, 4800)
	if f := st.Formats[0]; f.Decimals != 2 || f.Type != "B" || f.Min != 4000 || f.Max != 6000 {
		t.Errorf("format %+v", f)
	}
	if _, err := s.Next(ints(5000)); err == nil {
		t.Error("one value for two labels accepted")
	}
	if _, err := s.Next(ints(5000, 7000)); err == nil {
		t.Error("value above the maximum accepted")
	}
	st, err = s.Back()
	wantStep(t, st, err, "Capacity", 150) // the earlier answer is offered
	st, err = s.Next(ints(150))
	wantStep(t, st, err, "Volts", 5000, 4800)
	st, err = s.Next(ints(5200, 4900))
	wantStep(t, st, err, "Done", 0)
	st, err = s.Next(ints(0))
	wantStep(t, st, err, "Oops")
	if _, err := s.Next(nil); err == nil {
		t.Error("error step answered")
	}
	st, err = s.Back()
	wantStep(t, st, err, "Done", 0)
	st, err = s.Next(ints(1))
	if err != nil || st != nil || s.Current() != nil {
		t.Fatalf("not done: %+v %v", st, err)
	}

	run, err := p.Assemble(Options{State: Build, Answers: s.Answers()})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x44, 0x50, 0x14, 0x44, 0x24, 0x13, 2, 150, 8, 2}
	if !bytes.Equal(run.Code, want) {
		t.Errorf("code % x, want % x", run.Code, want)
	}

	sum, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, st := range sum.Steps {
		if st.Listed {
			listed = append(listed, st.Name)
		}
	}
	if strings.Join(listed, ",") != "Battery,Capacity" {
		t.Errorf("summary %v", listed)
	}

	// Read back from the image: the voltages come from the code.
	img := append(append([]byte(nil), run.Code...), 0xFF, 0xFF)
	ans, rb, err := p.ReadBack(img)
	if err != nil {
		t.Fatal(err)
	}
	if got := ans[Key{"synth", "volts"}]; len(got) != 2 || got[0].I != 5200 || got[1].I != 4900 {
		t.Errorf("read back volts %v", got)
	}
	if !bytes.HasPrefix(rb.Code, run.Code[:6]) {
		t.Errorf("read-back code % x", rb.Code)
	}
}

func TestBuildErrorStep(t *testing.T) {
	// Every step at its default: Done 0 reaches the error step, which
	// stops a build.
	_, err := synthProgram().Assemble(Options{State: DefaultBuild})
	var ue *asm.UserError
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "Oops") {
		t.Errorf("err = %v", err)
	}
	ans := Answers{{"synth", "done"}: ints(1)}
	run, err := synthProgram().Assemble(Options{State: Build, Answers: ans})
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0x44, 0x88, 0x13, 0x44, 0xc0, 0x12, 1, 0}; !bytes.HasPrefix(run.Code, want) {
		t.Errorf("code % x", run.Code)
	}
}

func TestDirectiveErrors(t *testing.T) {
	for _, src := range []string{
		"s .AID S, X\n",
		"s .AID S, R\n",
		"s .AID S, R, 2, 2\n",
		" .AUE nostep, x\n",
		"s .AID S, E, '0N 1 2'\n",
		"x .ALS 'text'\n",
	} {
		p := &Program{Main: "t", Source: []byte(src)}
		if _, err := p.Assemble(Options{State: Build}); err == nil {
			t.Errorf("%q accepted", src)
		}
	}
	p := &Program{Main: "t", Source: []byte(" .AERROR 'no RAM'\n")}
	if _, err := p.Assemble(Options{State: Build}); err == nil || !strings.Contains(err.Error(), "no RAM") {
		t.Errorf(".AERROR: %v", err)
	}
	p = &Program{Main: "t", Source: []byte(" .AMESSG 'hi'\nx .ALS 7\nx .ALS 9\ny .ALS D\n DATA x, $AssemblerState\n")}
	run, err := p.Assemble(Options{State: Build})
	if err != nil || len(run.Messages) != 1 || !bytes.Equal(run.Code, []byte{7, Build}) {
		t.Errorf(".AMESSG/.ALS: %+v %v", run, err)
	}
	p = &Program{Main: "t", Source: []byte(" .RESTART\n")}
	if _, err := p.Assemble(Options{State: Build}); err == nil {
		t.Error("endless .RESTART accepted")
	}
	p = &Program{Main: "t", Source: []byte("x .ALD RB1(0)\n DATA 1\n")}
	if _, err := p.Assemble(Options{State: BackReadBuild}); !errors.Is(err, asm.ErrUnknown) {
		t.Errorf("read-back without image: %v", err)
	}
}

func TestEdit(t *testing.T) {
	files := map[string]string{
		"A": "s .AID S, E, '0N 0 9 1'\n .AUE s, a\n DATA a\n",
		"B": "s .AID S, E, '0N 0 9 2'\n .AUE s, b\n DATA b\n",
	}
	p := &Program{Main: "main", Source: []byte(" .SOURCE A\n .SOURCE B\n"),
		Open: func(n string) ([]byte, error) { return []byte(files[n]), nil }}
	s := p.NewSession(Answers{{"A", "s"}: ints(5)})
	s.Edit = "B"
	st, err := s.Start()
	if err != nil || st == nil || st.Source != "B" {
		t.Fatalf("step %+v %v", st, err)
	}
	if st, err = s.Next(ints(7)); err != nil || st != nil {
		t.Fatalf("after B: %+v %v", st, err)
	}
	run, err := p.Assemble(Options{State: Build, Answers: s.Answers()})
	if err != nil || !bytes.Equal(run.Code, []byte{5, 7}) {
		t.Errorf("code %v %v", run, err)
	}
}

func TestTextAndFormat(t *testing.T) {
	tx := ParseText([]byte("ignored\r\n#One\r\nline 1\r\nline 2\r\n#two \r\n"))
	if tx.Section("ONE") != "line 1\nline 2" || tx.Section("two") != "" || len(tx) != 2 {
		t.Errorf("text %q", tx)
	}
	f, err := ParseFormat("2N 5000 6500 5020")
	if err != nil || f.Decimals != 2 || f.Type != "N" || f.Default != 5020 {
		t.Fatalf("%+v %v", f, err)
	}
	if f.Show(5020) != "50.20" || f.Show(-5) != "-0.05" {
		t.Errorf("Show %s %s", f.Show(5020), f.Show(-5))
	}
	for in, want := range map[string]int64{"50.2": 5020, "52.70": 5270, "53": 5300} {
		if got, err := f.Parse(in); err != nil || got != want {
			t.Errorf("Parse(%s) = %d %v", in, got, err)
		}
	}
	for _, bad := range []string{"49.99", "50.123", "x", ""} {
		if _, err := f.Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
	if f, err := ParseFormat("LN 0 3 1 extra"); err != nil || f.Type != "LN" || f.Decimals != 0 || len(f.Extra) != 1 {
		t.Errorf("%+v %v", f, err)
	}
	if _, err := ParseFormat("0N 1 x 2"); err == nil {
		t.Error("bad number accepted")
	}
}

func TestEncode(t *testing.T) {
	imm := func(v int) Operand { return Operand{Value: v} }
	mem := func(v int) Operand { return Operand{Value: v, Mem: true} }
	tests := []struct {
		mn   string
		ops  []Operand
		want []byte
	}{
		// The forms quoted in the spec.
		{"LOADW", []Operand{imm(5000)}, []byte{0x44, 0x88, 0x13}},
		{"LOADW", []Operand{imm(1)}, []byte{0x45, 0x01}},
		{"LOADW", []Operand{mem(0x9065)}, []byte{0x46, 0x65, 0x90}},
		{"LOADW", []Operand{mem(0)}, []byte{0x47, 0x00}},
		{"JMP", []Operand{imm(0)}, []byte{0x55, 0x00, 0x64}},
		{"JMP", []Operand{imm(0x2C1)}, []byte{0x54, 0xC1, 0x02, 0x64}},
		{"NOP", nil, []byte{0xC6}},
		{"WAITNP", nil, []byte{0xFE}},
		{"RET", nil, []byte{0xFF}},
		// One per remaining base.
		{"ADD", []Operand{imm(0x100)}, []byte{0x00, 0x00, 0x01}},
		{"ADDC", []Operand{imm(1)}, []byte{0x05, 0x01}},
		{"SUB", []Operand{imm(2)}, []byte{0x09, 0x02}},
		{"RSUB", []Operand{imm(3)}, []byte{0x0D, 0x03}},
		{"AND", []Operand{imm(4)}, []byte{0x11, 0x04}},
		{"IOR", []Operand{imm(5)}, []byte{0x15, 0x05}},
		{"XOR", []Operand{imm(6)}, []byte{0x19, 0x06}},
		{"ROL", []Operand{imm(7)}, []byte{0x1D, 0x07}},
		{"STOREW", []Operand{mem(0x12)}, []byte{0x41, 0x12}},
		{"MUL", []Operand{imm(0x1234)}, []byte{0x48, 0x34, 0x12}},
		{"DIV", []Operand{imm(10)}, []byte{0x4D, 0x0A}},
		{"MOV", []Operand{mem(0x10), imm(0x1234)}, []byte{0x54, 0x10, 0x34, 0x12}},
		{"MOV", []Operand{mem(0x10), {Value: 0x05, Indirect: true}}, []byte{0x57, 0x10, 0x85}},
		{"CMP", []Operand{mem(0x1000), imm(1)}, []byte{0x59, 0x00, 0x10, 0x01}},
		{"CALL", []Operand{imm(0x2C1)}, []byte{0x62, 0xC1}},
	}
	for _, tc := range tests {
		got, err := Encode(tc.mn, tc.ops...)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Errorf("%s %v = % x %v, want % x", tc.mn, tc.ops, got, err, tc.want)
			continue
		}
		if tc.mn != "JMP" && !Is(tc.mn, got[0]) {
			t.Errorf("Is(%s, %#x) false", tc.mn, got[0])
		}
	}
	if Is("LOADW", 0x50) || Is("NOSUCH", 0) {
		t.Error("Is matched the wrong opcode")
	}
	for _, bad := range [][]Operand{{{Value: 0x80, Indirect: true}}, {}} {
		if _, err := Encode("LOADW", bad...); err == nil {
			t.Errorf("LOADW %v accepted", bad)
		}
	}
	if _, err := Encode("CALL", imm(0x1000)); err == nil {
		t.Error("CALL beyond 12 bits accepted")
	}
	if v, err := operandAt([]byte{0x45, 7}, 0); err != nil || v != 7 {
		t.Errorf("operandAt byte %d %v", v, err)
	}
	if v, err := operandAt([]byte{0x44, 0x88, 0x13}, 0); err != nil || v != 5000 {
		t.Errorf("operandAt word %d %v", v, err)
	}
	if _, err := operandAt([]byte{0x44, 0x88}, 0); err == nil {
		t.Error("short operand accepted")
	}
}

func TestReadBackFunctions(t *testing.T) {
	img := []byte{0x44, 0x88, 0x13, 0xC6, 0xFF}
	p := &Program{Main: "t", Source: []byte(`
a .ALD RB1(3)
b .ALD RB2(1)
c .ALD RBO(0)
d .ALD RB_LOADW(0) AND RB_NOP(3) AND RB_RET(4) AND !RB_MOV(0)
s .AID S, E, '0N 0 255 0', '0N 0 65535 0', '0N 0 65535 0', '0N 0 1 0'
 .AUE s, a, b, c, d
`)}
	ans, _, err := p.ReadBack(img)
	if err != nil {
		t.Fatal(err)
	}
	got := ans[Key{"t", "s"}]
	if len(got) != 4 || got[0].I != 0xC6 || got[1].I != 5000 || got[2].I != 5000 || got[3].I != 1 {
		t.Errorf("read back %v", got)
	}
	p.Source = []byte("x .ALD RB2(4)\n")
	if _, _, err := p.ReadBack(img); err == nil {
		t.Error("read beyond the image accepted")
	}
}
