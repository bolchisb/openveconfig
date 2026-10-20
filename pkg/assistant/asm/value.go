package asm

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kind is the type of a Value.
type Kind uint8

// Value kinds (reverse-engineered, #8: integer, boolean, float, string and
// list operands).
const (
	Int Kind = iota
	Bool
	Float
	Str
	List
)

// Value is a calculator value.
type Value struct {
	K Kind
	I int64 // Int; Bool as 0/1
	F float64
	S string
	L []Value
}

// IntVal, BoolVal, FloatVal, StrVal and ListVal make values.
func IntVal(i int64) Value     { return Value{K: Int, I: i} }
func FloatVal(f float64) Value { return Value{K: Float, F: f} }
func StrVal(s string) Value    { return Value{K: Str, S: s} }
func ListVal(l ...Value) Value { return Value{K: List, L: l} }
func BoolVal(b bool) Value {
	if b {
		return Value{K: Bool, I: 1}
	}
	return Value{K: Bool}
}

// String formats v as str() does: integers in decimal, booleans as
// TRUE/FALSE (reverse-engineered, #8), strings as they are, lists as
// [a,b].
func (v Value) String() string {
	switch v.K {
	case Int:
		return strconv.FormatInt(v.I, 10)
	case Bool:
		if v.I != 0 {
			return "TRUE"
		}
		return "FALSE"
	case Float:
		return strconv.FormatFloat(v.F, 'g', -1, 64)
	case Str:
		return v.S
	}
	parts := make([]string, len(v.L))
	for i, e := range v.L {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Equal reports whether a and b are the same value.
func (v Value) Equal(w Value) bool {
	if v.K != w.K {
		if v.isNum() && w.isNum() {
			return v.float() == w.float()
		}
		return false
	}
	switch v.K {
	case Float:
		return v.F == w.F
	case Str:
		return v.S == w.S
	case List:
		if len(v.L) != len(w.L) {
			return false
		}
		for i := range v.L {
			if !v.L[i].Equal(w.L[i]) {
				return false
			}
		}
		return true
	}
	return v.I == w.I
}

func (v Value) isNum() bool { return v.K == Int || v.K == Float }

func (v Value) float() float64 {
	if v.K == Float {
		return v.F
	}
	return float64(v.I)
}

// AsInt returns v as an integer: an Int, a Bool (0/1) or a Float
// truncated.
func (v Value) AsInt() (int64, error) {
	switch v.K {
	case Int, Bool:
		return v.I, nil
	case Float:
		return int64(math.Trunc(v.F)), nil
	}
	return 0, fmt.Errorf("%w: %s is not a number", ErrType, v.kindName())
}

// AsBool returns v as a condition: a Bool, or an Int that is not 0.
func (v Value) AsBool() (bool, error) {
	switch v.K {
	case Bool, Int:
		return v.I != 0, nil
	}
	return false, fmt.Errorf("%w: %s is not a condition", ErrType, v.kindName())
}

func (v Value) kindName() string {
	return [...]string{"integer", "boolean", "float", "string", "list"}[v.K]
}
