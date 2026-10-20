package asm

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The sources below are synthetic, written for these tests.

func assemble(t *testing.T, a *Assembler, src string) []byte {
	t.Helper()
	res, err := a.Assemble("test", []byte(src))
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return res.Code
}

func wantCode(t *testing.T, got []byte, want ...byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("code = % x, want % x", got, want)
	}
}

func TestParseLine(t *testing.T) {
	tests := []struct{ in, lbl, mnem, ops string }{
		{"", "", "", ""},
		{"; only a comment", "", "", ""},
		{" DATA 1, 2 ; two bytes", "", "DATA", "1, 2"},
		{"start: DATA 1", "start", "DATA", "1"},
		{"start DATA 1", "start", "DATA", "1"},
		{"start:", "start", "", ""},
		{"f(a, b) .EQU a+b", "f(a, b)", ".EQU", "a+b"},
		{".IF x", "", ".IF", "x"},
		{"\tDATA ';', 2", "", "DATA", "';', 2"},
	}
	for _, tc := range tests {
		l, m, o := parseLine(tc.in)
		if l != tc.lbl || m != tc.mnem || o != tc.ops {
			t.Errorf("parseLine(%q) = %q %q %q", tc.in, l, m, o)
		}
	}
	if got := splitOps("a, f(b, c), [d, e], 'x,y'"); strings.Join(got, "|") != "a|f(b, c)|[d, e]|'x,y'" {
		t.Errorf("splitOps = %q", got)
	}
}

func TestData(t *testing.T) {
	got := assemble(t, &Assembler{}, `
 DATA 1, 0x1FF, 'AB', [3, 4], true
 DATA $`)
	wantCode(t, got, 1, 0xFF, 'A', 'B', 3, 4, 1, 7)
}

func TestLabelsAndForwardReferences(t *testing.T) {
	a := &Assembler{}
	res, err := a.Assemble("test", []byte(`
 DATA after, after >> 8   ; used before it is defined
here: DATA here
 DATA size
after
size .EQU after - here
`))
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, res.Code, 4, 0, 2, 2)
	if res.Passes < 2 {
		t.Errorf("passes = %d", res.Passes)
	}
	if v, ok := res.Label("SIZE"); !ok || v.I != 2 {
		t.Errorf("size = %v %v", v, ok)
	}
}

func TestEquSet(t *testing.T) {
	got := assemble(t, &Assembler{Labels: map[string]Value{"hwDC": IntVal(48)}}, `
volt .EQU hwDC*1300/12
n .SET 1
 DATA volt, volt >> 8, n
n .SET n + 1
 DATA n
b .SET 0x1FE, 8, false
c .SET 0xFE, 8, true
 DATA b, c = -2
`)
	wantCode(t, got, 0x50, 0x14, 1, 2, 0xFE, 1)

	_, err := (&Assembler{}).Assemble("test", []byte("x .EQU 1\nx .EQU 2\n"))
	if err == nil || !strings.Contains(err.Error(), "already defined") {
		t.Errorf("redefined .EQU: %v", err)
	}
	if _, err := (&Assembler{}).Assemble("t", []byte("x .EQU 1\nx .EQU 1\n")); err != nil {
		t.Errorf("same value again: %v", err)
	}
	got = assemble(t, &Assembler{}, ".SET y, 3\n DATA y\ny2 DATA 9\n")
	wantCode(t, got, 3, 9)
}

func TestUnset(t *testing.T) {
	_, err := (&Assembler{}).Assemble("t", []byte("x .SET 1\n .UNSET x\n DATA x\n"))
	if !errors.Is(err, ErrUnknown) {
		t.Errorf("after .UNSET: %v", err)
	}
	got := assemble(t, &Assembler{}, "x .SET 1\n .UNSET x\n .IF known(x)\n DATA 1\n .ELSE\n DATA 2\n .ENDIF\n")
	wantCode(t, got, 2)
}

func TestFunctionLabels(t *testing.T) {
	got := assemble(t, &Assembler{Labels: map[string]Value{"hwDC": IntVal(48)}}, `
Bat(v) .EQU hwDC*v/12
MMD(lo, hi, def) .EQU '2B '+str(Bat(lo))+' '+str(Bat(hi))+' '+str(Bat(def))
MAX(a, b) .EQU copy(str(a), 1, 0) + ''
acc .SET 0
ADDTO(x) .EQU acc := acc + x
 DATA Bat(1300), Bat(1300) >> 8
 DATA MMD(1200, 1300, 1250)
 DATA ADDTO(5), ADDTO(7), acc
`)
	want := append([]byte{0x50, 0x14}, "2B 4800 5200 5000"...)
	wantCode(t, got, append(want, 5, 12, 12)...)
}

func TestConditionals(t *testing.T) {
	got := assemble(t, &Assembler{}, `
x .EQU 2
 .IF x = 2
  DATA 1
  .IF x > 5
   DATA 99
  .ELSE
   DATA 2
  .ENDIF
 .ELSE
  DATA 98
  .IF 1
   DATA 97
  .ELSE
   DATA 96
  .ENDIF
 .ENDIF
 .IFNOT x = 2
  DATA 95
 .ENDIF
 .IFDEF x
  DATA 3
 .ENDIF
 .IFDEF later   ; known only from the previous pass
  DATA 94
 .ELSE
  DATA 4
 .ENDIF
 .IFNDEF nothere
  DATA 5
 .ENDIF
later .EQU 1
`)
	wantCode(t, got, 1, 2, 3, 4, 5)
	for _, src := range []string{" .ENDIF\n", " .ELSE\n", " .IF 1\n", " .ENDW\n", " .ENDM\n", " .WHILE 1\n"} {
		if _, err := (&Assembler{}).Assemble("t", []byte(src)); !errors.Is(err, ErrSyntax) {
			t.Errorf("%q: %v", src, err)
		}
	}
}

func TestWhile(t *testing.T) {
	got := assemble(t, &Assembler{}, `
i .SET 0
 .WHILE i < 3
j .SET 0
  .WHILE j < 2
   DATA i*16+j
j .SET j + 1
  .ENDW
i .SET i + 1
 .ENDW
 .WHILE false
  DATA 99
 .ENDW
`)
	wantCode(t, got, 0x00, 0x01, 0x10, 0x11, 0x20, 0x21)
	_, err := (&Assembler{}).Assemble("t", []byte(" .WHILE true\n .ENDW\n"))
	if err == nil || !strings.Contains(err.Error(), "1024") {
		t.Errorf("endless loop: %v", err)
	}
}

func TestMacros(t *testing.T) {
	got := assemble(t, &Assembler{}, `
; LOADI #value: a made-up two-form instruction, like the source's opcode
; macros (one byte for 0..255, two otherwise).
LOADI .MACRO 1, s:op
v .SET val(copy(op, 2, len(op)))
 .IF v <= 0xFF
  DATA 0x45, v
 .ELSE
  DATA 0x44, v, v >> 8
 .ENDIF
 .ENDM
PAIR .MACRO a, b
 DATA Params, a
 .IFDEF b
  DATA b
 .ENDIF
 .ENDM
SKIP .MACRO
 .LOCAL over
 DATA 0x54, over, over >> 8
 DATA 0xEE
over
 .ENDM
 LOADI #5
 LOADI #0x1388
 PAIR 7
 PAIR 7, 8
top SKIP
 SKIP
 DATA top
`)
	wantCode(t, got,
		0x45, 5, 0x44, 0x88, 0x13,
		1, 7, 2, 7, 8,
		0x54, 14, 0, 0xEE,
		0x54, 18, 0, 0xEE,
		10)
	if _, err := (&Assembler{}).Assemble("t", []byte("M .MACRO 2, a, b\n .ENDM\n M 1\n")); !errors.Is(err, ErrSyntax) {
		t.Errorf("too few arguments: %v", err)
	}
	if _, err := (&Assembler{}).Assemble("t", []byte("M .MACRO\n DATA 1\n")); !errors.Is(err, ErrSyntax) {
		t.Errorf("no .ENDM: %v", err)
	}
	if _, err := (&Assembler{}).Assemble("t", []byte(" NOSUCH 1\n")); !errors.Is(err, ErrSyntax) {
		t.Errorf("unknown mnemonic: %v", err)
	}
}

func TestRenMacro(t *testing.T) {
	got := assemble(t, &Assembler{}, `
A .MACRO
 DATA 1
 .ENDM
 .RENMACRO A, B
 B
B .MACRO
 DATA 2
 .ENDM
 B
 .RENMACRO B, B
`)
	wantCode(t, got, 1, 2)
}

func TestAssembleDirective(t *testing.T) {
	got := assemble(t, &Assembler{}, `
i .SET 0
 .WHILE i < 3
  .ASSEMBLE ' DATA '+str(i+1)
i .SET i + 1
 .ENDW
 .ASSEMBLE 'lbl DATA 9'
 DATA lbl
`)
	wantCode(t, got, 1, 2, 3, 9, 3)
}

func TestMessages(t *testing.T) {
	res, err := (&Assembler{}).Assemble("t", []byte(" .MESSG 'hello '+str(1)\n .MESSG plain text here\n"))
	if err != nil || len(res.Messages) != 2 || res.Messages[0] != "hello 1" || res.Messages[1] != "plain text here" {
		t.Errorf("messages %v %v", res, err)
	}
	_, err = (&Assembler{}).Assemble("t", []byte(" DATA 1\n .ERROR 'Not enough RAM'\n DATA 2\n"))
	var ue *UserError
	var pe *PosError
	if !errors.As(err, &ue) || ue.Msg != "Not enough RAM" || !errors.As(err, &pe) || pe.Pos != "t:2" {
		t.Errorf(".ERROR: %v", err)
	}
	res, err = (&Assembler{}).Assemble("t", []byte(" DATA 1\n .FERROR 'stop'\n DATA 2\n"))
	if !errors.As(err, &ue) || ue.Msg != "stop" || len(res.Code) != 1 {
		t.Errorf(".FERROR: %v %v", res, err)
	}
	if _, err := (&Assembler{}).Assemble("t", []byte(" .NOP\n .LIST\n .NOLIST\n .DEFER\n .NODEFER\n .EXT x\n")); err != nil {
		t.Error(err)
	}
}

func TestSourceAndInsert(t *testing.T) {
	files := map[string]string{
		"lib": `
 .GLOBAL True
Shared .EQU 0x22
 .GLOBAL False
private .EQU 1
pub2 .EQU 0x33
 .PUB pub2
EMIT .MACRO x
 DATA x
 .ENDM
`,
		"inc":  "incl .EQU 5\n",
		"user": " EMIT Shared\n EMIT pub2\n .IFNDEF private\n DATA 0x44\n .ENDIF\nprivate .EQU 2\n DATA private\n",
	}
	a := &Assembler{Open: func(name string) ([]byte, error) {
		s, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("no file %s", name)
		}
		return []byte(s), nil
	}}
	got := assemble(t, a, " .SOURCE lib\n .SOURCE user\n .INSERT inc\n DATA incl\n .INSERT 'in'+'c'\n")
	wantCode(t, got, 0x22, 0x33, 0x44, 2, 5)
	if _, err := a.Assemble("t", []byte(" .SOURCE missing\n")); err == nil {
		t.Error("missing file")
	}
}

func TestPredefinedAndDollarLabels(t *testing.T) {
	got := assemble(t, &Assembler{Labels: map[string]Value{"cX": IntVal(3)}}, `
 DATA cx, $, $AssemblerPass > 1
 DATA len($CODE)
`)
	wantCode(t, got, 3, 1, 1, 3)
}

func TestExtensions(t *testing.T) {
	var seen []string
	a := &Assembler{
		Directives: map[string]Directive{
			".TWICE": func(c *Ctx, lbl string, ops []string) error {
				v, _, err := c.Eval(ops[0])
				if err != nil {
					return err
				}
				seen = append(seen, lbl)
				return c.Define(lbl, IntVal(2*v.I))
			},
			".HALT": func(c *Ctx, lbl string, ops []string) error { return ErrStop },
		},
		Funcs: map[string]Func{
			"Triple": func(c *Ctx, args []Value) (Value, error) { return IntVal(3 * args[0].I), nil },
		},
	}
	res, err := a.Assemble("t", []byte("x .TWICE 4\n DATA x, triple(x)\n .HALT\n DATA 1\n"))
	if !errors.Is(err, ErrStop) {
		t.Fatalf("err = %v", err)
	}
	wantCode(t, res.Code, 8, 24)
	if len(seen) < 2 || seen[0] != "x" {
		t.Errorf("seen %v", seen)
	}
	a.Directives[".RESTART"] = func(c *Ctx, lbl string, ops []string) error { return ErrRestart }
	if _, err := a.Assemble("t", []byte(" .RESTART\n")); !errors.Is(err, ErrRestart) {
		t.Errorf("restart: %v", err)
	}
}

func TestNoConvergence(t *testing.T) {
	// x flips every pass.
	_, err := (&Assembler{}).Assemble("t", []byte(" .IFDEF x\n .ELSE\n DATA 0\n .ENDIF\n .IF $ = 0\nx .EQU 1\n .ENDIF\n"))
	if err != nil {
		// Either it settles or reports that it does not; it must not hang.
		t.Log(err)
	}
	_, err = (&Assembler{}).Assemble("t", []byte(" DATA y\ny .EQU y + 1\n"))
	if err == nil {
		t.Error("self-referencing label accepted")
	}
}

func TestUnknownLabelError(t *testing.T) {
	_, err := (&Assembler{}).Assemble("t", []byte(" DATA 1\n DATA nothere\n"))
	var pe *PosError
	if !errors.Is(err, ErrUnknown) || !errors.As(err, &pe) || pe.Pos != "t:2" || !strings.Contains(err.Error(), "nothere") {
		t.Errorf("err = %v", err)
	}
}
