package asm

import (
	"errors"
	"testing"
)

// mapScope is a scope over a plain map, for calculator tests.
type mapScope map[string]Value

func (m mapScope) lookup(name string) (Value, error) {
	if v, ok := m[key(name)]; ok {
		return v, nil
	}
	return Value{}, ErrUnknown
}
func (m mapScope) assign(name string, v Value) error { m[key(name)] = v; return nil }
func (m mapScope) call(string, []Value) (Value, bool, error) {
	return Value{}, false, nil
}
func (m mapScope) eval(expr string) (Value, error) { return evalExpr(m, expr) }

func TestTokenize(t *testing.T) {
	toks, err := tokenize(`a1_$#x + 0x1F*0b101 >= 'it''s'#13#$0A and`)
	if err != nil {
		t.Fatal(err)
	}
	want := []token{
		{k: tIdent, s: "a1_$#x"}, {k: tOp, s: "+"}, {k: tNum, v: IntVal(31)}, {k: tOp, s: "*"},
		{k: tNum, v: IntVal(5)}, {k: tOp, s: ">="}, {k: tStr, s: "it's\r\n"}, {k: tOp, s: "AND"}, {k: tEOF},
	}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens: %+v", len(toks), toks)
	}
	for i := range want {
		if toks[i].k != want[i].k || toks[i].s != want[i].s || !toks[i].v.Equal(want[i].v) {
			t.Errorf("token %d = %+v, want %+v", i, toks[i], want[i])
		}
	}
	for _, bad := range []string{"'open", "12ab", "a ? b", "#300"} {
		if _, err := tokenize(bad); !errors.Is(err, ErrSyntax) {
			t.Errorf("tokenize(%q) = %v", bad, err)
		}
	}
}

func TestCalculator(t *testing.T) {
	sc := mapScope{"hwdc": IntVal(48), "s": StrVal("hello"), "l": ListVal(IntVal(7), IntVal(8))}
	tests := []struct {
		expr string
		want Value
	}{
		{"1 + 2 * 3", IntVal(7)},
		{"(1 + 2) * 3", IntVal(9)},
		{"7 / 2", IntVal(3)},
		{"-7 / 2", IntVal(-3)},
		{"7 MOD 3", IntVal(1)},
		{"2 ^ 3 ^ 2", IntVal(512)},
		{"1 << 4 >> 2", IntVal(4)},
		{"0xF0 AND 0x3C OR 1", IntVal(0x31)},
		{"0xFF XOR 0x0F", IntVal(0xF0)},
		{"hwDC*1300/12", IntVal(5200)},
		{"1 = 1 AND 2 != 3", BoolVal(true)},
		{"3 >= 4 OR 2 <= 2", BoolVal(true)},
		{"!(1 < 2)", BoolVal(false)},
		{"!0", IntVal(-1)},
		{"-hwDC", IntVal(-48)},
		{"true XOR FALSE", BoolVal(true)},
		{"'2B '+str(hwDC*1300/12)", StrVal("2B 5200")},
		{"str(1=1)", StrVal("TRUE")},
		{"1.5 * 2", FloatVal(3)},
		{"'a' < 'b'", BoolVal(true)},
		{"s@1 + s@5", StrVal("ho")},
		{"l@1", IntVal(8)},
		{"[] + [1] + 2", ListVal(IntVal(1), IntVal(2))},
		{"len(l + [1, 2])", IntVal(4)},
		{"[1, 'x'] = [1, 'x']", BoolVal(true)},
		{"int(7.9)", IntVal(7)},
		{"abs(-3) + sign(-9)", IntVal(2)},
		{"copy(s, 2, 3)", StrVal("ell")},
		{"copy(s, 4)", StrVal("lo")},
		{"copy(s, 9, 2)", StrVal("")},
		{"del(s, 1, 2)", StrVal("llo")},
		{"ins('XY', s, 3)", StrVal("heXYllo")},
		{"pos('ll', s)", IntVal(3)},
		{"pos('z', s)", IntVal(0)},
		{"len(s)", IntVal(5)},
		{"val('2*hwDC')", IntVal(96)},
		{"ord('A')", IntVal(65)},
		{"hex(255)", StrVal("FF")},
		{"hex(10, 4)", StrVal("000A")},
		{"known(hwDC)", BoolVal(true)},
		{"known(nothere + 1)", BoolVal(false)},
		{"Round(2.5) + Trunc(-1.7)", IntVal(1)},
		{"Sqrt(16)", FloatVal(4)},
		{"x := 5 + 1", IntVal(6)},
	}
	for _, tc := range tests {
		got, err := evalExpr(sc, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if !got.Equal(tc.want) || got.K != tc.want.K {
			t.Errorf("%s = %v (%s), want %v (%s)", tc.expr, got, got.kindName(), tc.want, tc.want.kindName())
		}
	}
	if sc["x"].I != 6 {
		t.Errorf(":= did not assign: %v", sc["x"])
	}
}

func TestCalculatorErrors(t *testing.T) {
	sc := mapScope{}
	tests := []struct {
		expr string
		err  error
	}{
		{"1 / 0", ErrDivZero},
		{"1 MOD 0", ErrDivZero},
		{"'a' + 1", ErrType},
		{"-'a'", ErrType},
		{"undefined + 1", ErrUnknown},
		{"nofunc(1)", ErrUnknown},
		{"(1 + 2", ErrSyntax},
		{"1 2", ErrSyntax},
		{"copy(1)", ErrSyntax},
	}
	for _, tc := range tests {
		if _, err := evalExpr(sc, tc.expr); !errors.Is(err, tc.err) {
			t.Errorf("%s: %v, want %v", tc.expr, err, tc.err)
		}
	}
	var ue *UserError
	if _, err := evalExpr(sc, "err('no RAM')"); !errors.As(err, &ue) || ue.Msg != "no RAM" {
		t.Errorf("err(): %v", err)
	}
	if ErrDivZero.Error() != "Division by ZERO" {
		t.Error("division message")
	}
}
