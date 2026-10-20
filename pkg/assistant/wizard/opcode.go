package wizard

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// The assistant byte code (reverse-engineered, #8; restated in our own
// words). The sources define the instructions themselves, as macros that
// emit DATA bytes, so the assembler does not need this table; it serves
// the read-back functions, which recognise instructions in an image.
//
// An operand has two bits: bit 1 says it refers to memory (clear for an
// immediate, written "#value"), bit 0 that it fits in one byte. A one-byte
// operand is 1 byte, otherwise 2 (little-endian). "[addr]" is indirect: it
// sets 0x80 in the operand byte and needs addr ≤ 0x7F.
//
// What the spec leaves open, and is therefore not here: the jump group
// (0x80–0xBF, JP/JPZ/JPC/… and JPFC/JPFS), the bit set/clear group at
// 0xC0 (its stated encoding overlaps NOP), which of INC/INCTO/DEC/DECTO/
// CLAMPMAX/CLAMPMIN has which of the bases 0x20–0x3C, and the order of
// MOV's and CMP's two operands (dest first is inferred).

// Operand is an instruction operand.
type Operand struct {
	Value    int
	Mem      bool // a memory address, not an immediate
	Indirect bool // [addr]
}

// bits returns the operand's two-bit tag.
func (o Operand) bits() byte {
	var b byte
	if o.Mem || o.Indirect {
		b |= 2
	}
	if o.Indirect || o.Value >= 0 && o.Value <= 0xFF {
		b |= 1
	}
	return b
}

func (o Operand) bytes() ([]byte, error) {
	switch {
	case o.Indirect && (o.Value < 0 || o.Value > 0x7F):
		return nil, fmt.Errorf("indirect operand %#x above 0x7f", o.Value)
	case o.Indirect:
		return []byte{byte(o.Value) | 0x80}, nil
	case o.bits()&1 != 0:
		return []byte{byte(o.Value)}, nil
	}
	return binary.LittleEndian.AppendUint16(nil, uint16(o.Value)), nil
}

type opForm uint8

const (
	formOne     opForm = iota // base + (operand bits AND 1), operand
	formOneFull               // base + operand bits, operand
	formTwo                   // base + src bits + (dest bit 0 << 2), dest, src
	formNone                  // the byte alone
	formCall                  // base + high bits of a 12-bit target, low byte
)

type opcode struct {
	base, mask byte
	form       opForm
}

// opcodes are the instructions whose encoding the spec gives, with the
// mask that recognises them when read back.
var opcodes = map[string]opcode{
	"ADD": {0x00, 0xDC, formOne}, "ADDC": {0x04, 0xDC, formOne},
	"SUB": {0x08, 0xDC, formOne}, "RSUB": {0x0C, 0xDC, formOne},
	"AND": {0x10, 0xDC, formOne}, "IOR": {0x14, 0xDC, formOne},
	"XOR": {0x18, 0xDC, formOne}, "ROL": {0x1C, 0xDC, formOne},
	"STOREW": {0x40, 0xFE, formOne},
	"LOADW":  {0x44, 0xFC, formOneFull},
	"MUL":    {0x48, 0xFC, formOne},
	"DIV":    {0x4C, 0xFC, formOne},
	"MOV":    {0x50, 0xF8, formTwo},
	"CMP":    {0x58, 0xF8, formTwo},
	"CALL":   {0x60, 0xF0, formCall},
	"NOP":    {0xC6, 0xFF, formNone},
	"WAITNP": {0xFE, 0xFF, formNone},
	"RET":    {0xFF, 0xFF, formNone},
}

// Encode returns the bytes of an instruction. JMP addr is the address
// pushed as an immediate (0x54 lo hi, or 0x55 lo below 0x100) followed by
// the jump byte 0x64, as at the end of every program (reverse-engineered,
// #8).
func Encode(mnemonic string, ops ...Operand) ([]byte, error) {
	name := strings.ToUpper(mnemonic)
	if name == "JMP" {
		if len(ops) != 1 || ops[0].Mem {
			return nil, fmt.Errorf("JMP takes one address")
		}
		b, err := Operand{Value: ops[0].Value}.bytes()
		if err != nil {
			return nil, err
		}
		op := byte(0x54)
		if len(b) == 1 {
			op = 0x55
		}
		return append(append([]byte{op}, b...), 0x64), nil
	}
	oc, ok := opcodes[name]
	if !ok {
		return nil, fmt.Errorf("no encoding for %s", mnemonic)
	}
	want := map[opForm]int{formOne: 1, formOneFull: 1, formTwo: 2, formNone: 0, formCall: 1}[oc.form]
	if len(ops) != want {
		return nil, fmt.Errorf("%s takes %d operands", name, want)
	}
	switch oc.form {
	case formNone:
		return []byte{oc.base}, nil
	case formCall:
		t := ops[0].Value
		if t < 0 || t > 0xFFF {
			return nil, fmt.Errorf("CALL target %#x out of range", t)
		}
		return []byte{oc.base | byte(t>>8), byte(t)}, nil
	case formTwo:
		d, err := ops[0].bytes()
		if err != nil {
			return nil, err
		}
		s, err := ops[1].bytes()
		if err != nil {
			return nil, err
		}
		op := oc.base + ops[1].bits() + (ops[0].bits()&1)<<2
		return append(append([]byte{op}, d...), s...), nil
	}
	b, err := ops[0].bytes()
	if err != nil {
		return nil, err
	}
	bits := ops[0].bits()
	if oc.form == formOne {
		bits &= 1
	}
	return append([]byte{oc.base + bits}, b...), nil
}

// Is reports whether b is the opcode byte of mnemonic, by the read-back
// mask.
func Is(mnemonic string, b byte) bool {
	oc, ok := opcodes[strings.ToUpper(mnemonic)]
	return ok && b&oc.mask == oc.base
}

// operandAt returns the first operand of the one-operand instruction at
// img[x]: one byte when bit 0 of the opcode is set, else two.
func operandAt(img []byte, x int) (int, error) {
	if x < 0 || x+1 >= len(img) {
		return 0, fmt.Errorf("read-back offset %d outside the image", x)
	}
	if img[x]&1 != 0 {
		return int(img[x+1]), nil
	}
	if x+2 >= len(img) {
		return 0, fmt.Errorf("read-back offset %d outside the image", x)
	}
	return int(binary.LittleEndian.Uint16(img[x+1:])), nil
}
