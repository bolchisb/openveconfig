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
// (0x80–0xBF, JP/JPZ/JPC/… and JPFC/JPFS) and the bit set/clear group at
// 0xC0 (its stated encoding overlaps NOP).

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
	formOne   opForm = iota // base + (src bits AND 1), src; with ->dest see Encode
	formFull                // base + src bits, src
	formMul                 // base + src bits, src, shift byte
	formTwo                 // base + src bits + (dest bit 0 << 2), src, dest
	formLimit               // base + (bits AND 1), operand; alt base with a limit
	formNone                // the byte alone
	formCall                // base + high bits of a 12-bit target, low byte
)

type opcode struct {
	base  byte
	form  opForm
	alt   byte // formLimit: the base of the form with a limit
	alu   byte // formOne arithmetic: its type, for the ->dest form
	shift int  // formMul: the fixed shift, or -1 when it is an operand
}

// opcodes are the instructions whose encoding the spec gives (§3.3 with
// the corrections of its addendum, reverse-engineered, #8).
var opcodes = map[string]opcode{
	"ADD": {base: 0x00, alu: 0}, "ADDC": {base: 0x04, alu: 1},
	"SUB": {base: 0x08, alu: 2}, "RSUB": {base: 0x0C, alu: 3},
	"AND": {base: 0x10, alu: 4}, "IOR": {base: 0x14, alu: 5},
	"XOR": {base: 0x18, alu: 6}, "ROL": {base: 0x1C, alu: 7},
	"INC":      {base: 0x20, form: formLimit},
	"INCTO":    {base: 0x24, form: formLimit, alt: 0x28},
	"CLAMPMAX": {base: 0x2C, form: formLimit},
	"DEC":      {base: 0x30, form: formLimit},
	"DECTO":    {base: 0x34, form: formLimit, alt: 0x38},
	"CLAMPMIN": {base: 0x3C, form: formLimit},
	"STOREW":   {base: 0x40, form: formOne, alu: 0xFF},
	"LOADW":    {base: 0x44, form: formFull},
	"MUL":      {base: 0x48, form: formMul, shift: -1},
	"MULSHR":   {base: 0x48, form: formMul, shift: -1},
	"BAREMUL":  {base: 0x48, form: formMul, shift: 32},
	"DIV":      {base: 0x4C, form: formOne, alu: 0xFF},
	"MOV":      {base: 0x50, form: formTwo},
	"CMP":      {base: 0x58, form: formTwo},
	"CALL":     {base: 0x60, form: formCall},
	"NOP":      {base: 0xC6, form: formNone},
	"WAITNP":   {base: 0xFE, form: formNone},
	"RET":      {base: 0xFF, form: formNone},
}

// pcAddr is the address that JMP moves its target to: "JMP a" is the MOV
// of the immediate a to address 0x64, the bytes 54 lo hi 64 (or 55 lo 64)
// that end every program seen (reverse-engineered, #8; that 0x64 is the
// program counter is inferred from the pattern).
const pcAddr = 0x64

// Encode returns the bytes of an instruction. Operands are as written in
// the source, destination first: "MOV dest, src", "ADD src" or
// "ADD src, dest" for the ->dest form, "INCTO x[, limit]",
// "MUL src[, shift]" and "MULSHR src, shift" (MUL without a shift has
// shift 0, inferred), "BAREMUL src" (shift 32), "JMP addr".
//
// Inferred where the spec is silent: the ->dest form and the limit form
// put the second operand's bytes after the first's, and only the first
// operand's size reaches the opcode.
func Encode(mnemonic string, ops ...Operand) ([]byte, error) {
	name := strings.ToUpper(mnemonic)
	if name == "JMP" {
		if len(ops) != 1 || ops[0].Mem || ops[0].Indirect {
			return nil, fmt.Errorf("JMP takes one address")
		}
		return Encode("MOV", Operand{Value: pcAddr, Mem: true}, ops[0])
	}
	oc, ok := opcodes[name]
	if !ok {
		return nil, fmt.Errorf("no encoding for %s", mnemonic)
	}
	cat := func(op byte, os ...Operand) ([]byte, error) {
		out := []byte{op}
		for _, o := range os {
			b, err := o.bytes()
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
		}
		return out, nil
	}
	nops := func(lo, hi int) error {
		if len(ops) < lo || len(ops) > hi {
			return fmt.Errorf("%s takes %d to %d operands", name, lo, hi)
		}
		return nil
	}
	switch oc.form {
	case formNone:
		if err := nops(0, 0); err != nil {
			return nil, err
		}
		return []byte{oc.base}, nil
	case formCall:
		if err := nops(1, 1); err != nil {
			return nil, err
		}
		t := ops[0].Value
		if t < 0 || t > 0xFFF {
			return nil, fmt.Errorf("CALL target %#x out of range", t)
		}
		return []byte{oc.base | byte(t>>8), byte(t)}, nil
	case formTwo: // MOV dest, src: the source's bytes come first
		if err := nops(2, 2); err != nil {
			return nil, err
		}
		return cat(oc.base+ops[1].bits()+(ops[0].bits()&1)<<2, ops[1], ops[0])
	case formFull:
		if err := nops(1, 1); err != nil {
			return nil, err
		}
		return cat(oc.base+ops[0].bits(), ops[0])
	case formMul:
		shift := oc.shift
		if shift < 0 {
			lo := 1
			if name == "MULSHR" {
				lo = 2
			}
			if err := nops(lo, 2); err != nil {
				return nil, err
			}
			shift = 0
			if len(ops) == 2 {
				shift = ops[1].Value
			}
		} else if err := nops(1, 1); err != nil {
			return nil, err
		}
		if shift < 0 || shift > 0xFF {
			return nil, fmt.Errorf("%s shift %d out of range", name, shift)
		}
		b, err := cat(oc.base+ops[0].bits(), ops[0])
		return append(b, byte(shift)), err
	case formLimit:
		max := 1
		if oc.alt != 0 {
			max = 2
		}
		if err := nops(1, max); err != nil {
			return nil, err
		}
		base := oc.base
		if len(ops) == 2 {
			base = oc.alt
		}
		return cat(base+ops[0].bits()&1, ops...)
	}
	// formOne: arithmetic, STOREW, DIV.
	if oc.alu != 0xFF && len(ops) == 2 { // src ->dest
		return cat(oc.alu<<2+0x22+ops[0].bits()&1, ops...)
	}
	if err := nops(1, 1); err != nil {
		return nil, err
	}
	return cat(oc.base+ops[0].bits()&1, ops[0])
}

// Is reports whether b is the opcode byte of mnemonic. Arithmetic matches
// both its plain and its ->dest form; INCTO and DECTO both their forms.
func Is(mnemonic string, b byte) bool {
	oc, ok := opcodes[strings.ToUpper(mnemonic)]
	if !ok {
		return false
	}
	switch oc.form {
	case formNone:
		return b == oc.base
	case formCall, formTwo:
		mask := map[opForm]byte{formCall: 0xF0, formTwo: 0xF8}[oc.form]
		return b&mask == oc.base
	case formFull, formMul:
		return b&0xFC == oc.base
	case formLimit:
		return b&0xFE == oc.base || oc.alt != 0 && b&0xFE == oc.alt
	}
	if oc.alu != 0xFF && b&0xFE == oc.alu<<2+0x22 {
		return true
	}
	return b&0xFE == oc.base
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
