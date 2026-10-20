package system

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Record is a unit's 9-byte system record, as read from and written to the
// unit. Reverse-engineered (#10):
//
//	byte 0     redundancy: the number of units in the unit's phase on a
//	           group leader (master), 0xFF on every other unit
//	byte 1     copy of byte 0
//	bytes 2-5  presence mask, u32 little endian: bit n set when short
//	           address n is in the system; the same in every unit
//	byte 6     bits 0-3 phase angle / 30°; bits 4-5 AC input index;
//	           bit 6 floating with return to the original phase;
//	           bit 7 floating (bits 6 and 7 only on L2/L3)
//	byte 7     bits 0-1 phase index (L1-L3); bits 2-3 number of phases - 1;
//	           bit 4 assistant-reset flag; bit 5 the second AC input is
//	           switched as a group (always 1 on single-input units);
//	           bit 6 set by every tool that writes a record;
//	           bit 7 the unit's AC input is switched as a group
//	byte 8     the unit's short address
//
// Fixed, floating and floating with return are byte 6 bits 7/6 = 00, 10
// and 11.
type Record [9]byte

const (
	b7PhaseMask   = 0x03
	b7PhasesShift = 2
	b7PhasesMask  = 0x0C
	b7Assistant   = 0x10
	b7Second      = 0x20
	b7Marker      = 0x40
	b7Grouped     = 0x80

	b6AngleMask = 0x0F
	b6ACShift   = 4
	b6ACMask    = 0x30
	b6Return    = 0x40
	b6Floating  = 0x80

	// NoLeader is the redundancy byte of a unit that does not lead its
	// group.
	NoLeader = 0xFF
	// NoAddress is the short address of a unit for which no address was
	// left (more than 32 units, which Check refuses).
	NoAddress = 0xFF
)

// factoryRecord is the record of a unit that was never configured, and the
// standalone record: L1, one phase, presence mask = address 0, address 0,
// both group flags on, marker set, assistant-reset flag clear.
// Reverse-engineered (#10); community files agree (kylehart FORMAT.md
// §3.1.1).
var factoryRecord = Record{0x01, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0xE0, 0x00}

// FactoryDefault returns the factory record with the assistant-reset flag
// copied from prev. A unit that reports that it has no system configuration
// (ResponseNoConfig) is treated as holding this record, keeping the flag
// it had before (reverse-engineered (#10)).
func FactoryDefault(prev Record) Record {
	r := factoryRecord
	r.SetAssistantResetFlag(prev.AssistantResetFlag())
	return r
}

// Redundancy returns byte 0: the number of units in the phase on a group
// leader, NoLeader on every other unit.
func (r Record) Redundancy() byte { return r[0] }

// RedundancyCopy returns byte 1, which every tool writes equal to byte 0.
func (r Record) RedundancyCopy() byte { return r[1] }

// SetRedundancy sets bytes 0 and 1 to n.
func (r *Record) SetRedundancy(n byte) { r[0], r[1] = n, n }

// Leader reports whether the record marks its unit as a group leader.
func (r Record) Leader() bool { return r[0] != NoLeader }

// PresenceMask returns bytes 2-5: bit n is set when short address n is in
// the system.
func (r Record) PresenceMask() uint32 { return binary.LittleEndian.Uint32(r[2:6]) }

// SetPresenceMask sets bytes 2-5.
func (r *Record) SetPresenceMask(m uint32) { binary.LittleEndian.PutUint32(r[2:6], m) }

// PhaseAngle returns the phase angle in degrees (byte 6 bits 0-3 × 30).
func (r Record) PhaseAngle() int { return int(r[6]&b6AngleMask) * 30 }

// SetPhaseAngle sets the phase angle. deg is rounded down to a multiple of
// 30 and must lie in 0..450; larger values are truncated to the 4-bit field.
func (r *Record) SetPhaseAngle(deg int) {
	r[6] = r[6]&^b6AngleMask | byte(deg/30)&b6AngleMask
}

// ACInput returns the AC input index 0-3 (byte 6 bits 4-5).
func (r Record) ACInput() int { return int(r[6]&b6ACMask) >> b6ACShift }

// SetACInput sets the AC input index; only its low two bits are stored.
func (r *Record) SetACInput(i int) {
	r[6] = r[6]&^b6ACMask | byte(i<<b6ACShift)&b6ACMask
}

// FloatingReturn reports byte 6 bit 6: floating with return to the
// original phase (together with Floating).
func (r Record) FloatingReturn() bool { return r[6]&b6Return != 0 }

// SetFloatingReturn sets byte 6 bit 6.
func (r *Record) SetFloatingReturn(on bool) { setBit(&r[6], b6Return, on) }

// Floating reports byte 6 bit 7.
func (r Record) Floating() bool { return r[6]&b6Floating != 0 }

// SetFloating sets byte 6 bit 7.
func (r *Record) SetFloating(on bool) { setBit(&r[6], b6Floating, on) }

// Phase returns the phase index (byte 7 bits 0-1): 0 = L1, 1 = L2, 2 = L3.
// 3 does not occur in a valid record.
func (r Record) Phase() int { return int(r[7] & b7PhaseMask) }

// SetPhase sets the phase index; only its low two bits are stored.
func (r *Record) SetPhase(p int) { r[7] = r[7]&^b7PhaseMask | byte(p)&b7PhaseMask }

// Phases returns the number of phases in the system (byte 7 bits 2-3,
// plus one).
func (r Record) Phases() int { return int(r[7]&b7PhasesMask)>>b7PhasesShift + 1 }

// SetPhases sets the number of phases, 1-4.
func (r *Record) SetPhases(n int) {
	r[7] = r[7]&^b7PhasesMask | byte((n-1)<<b7PhasesShift)&b7PhasesMask
}

// AssistantResetFlag reports byte 7 bit 4. See Unit.AssistantFlagApplies.
func (r Record) AssistantResetFlag() bool { return r[7]&b7Assistant != 0 }

// SetAssistantResetFlag sets byte 7 bit 4.
func (r *Record) SetAssistantResetFlag(on bool) { setBit(&r[7], b7Assistant, on) }

// SecondInputGrouped reports byte 7 bit 5: the unit's second AC input is
// switched as a group. It is always set on single-input units.
func (r Record) SecondInputGrouped() bool { return r[7]&b7Second != 0 }

// SetSecondInputGrouped sets byte 7 bit 5.
func (r *Record) SetSecondInputGrouped(on bool) { setBit(&r[7], b7Second, on) }

// Marker reports byte 7 bit 6, which every tool sets when it writes a
// record. A record without it does not belong to a valid system.
func (r Record) Marker() bool { return r[7]&b7Marker != 0 }

// SetMarker sets byte 7 bit 6.
func (r *Record) SetMarker(on bool) { setBit(&r[7], b7Marker, on) }

// Grouped reports byte 7 bit 7: the unit's AC input is switched as a group.
func (r Record) Grouped() bool { return r[7]&b7Grouped != 0 }

// SetGrouped sets byte 7 bit 7.
func (r *Record) SetGrouped(on bool) { setBit(&r[7], b7Grouped, on) }

// ShortAddress returns byte 8, the unit's VE.Bus short address.
func (r Record) ShortAddress() byte { return r[8] }

// SetShortAddress sets byte 8.
func (r *Record) SetShortAddress(a byte) { r[8] = a }

func setBit(b *byte, mask byte, on bool) {
	if on {
		*b |= mask
	} else {
		*b &^= mask
	}
}

// Hex returns the record as nine upper-case hex bytes separated by spaces.
func (r Record) Hex() string { return fmt.Sprintf("% X", r[:]) }

// String returns the record's bytes and a decoded one-line form, for
// example "02 02 09 00 00 00 00 F0 00 (L1 of 1, AC1, 0°, addr 0, leads 2,
// mask 00000009, grouped, 2nd grouped, reset-assistants, marker)".
func (r Record) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (L%d of %d, AC%d, %d°", r.Hex(), r.Phase()+1, r.Phases(), r.ACInput()+1, r.PhaseAngle())
	switch {
	case r.Floating() && r.FloatingReturn():
		b.WriteString(" floating with return")
	case r.Floating():
		b.WriteString(" floating")
	case r.FloatingReturn():
		b.WriteString(" return bit only")
	}
	fmt.Fprintf(&b, ", addr %d", r.ShortAddress())
	if r.Leader() {
		fmt.Fprintf(&b, ", leads %d", r.Redundancy())
	}
	fmt.Fprintf(&b, ", mask %08X", r.PresenceMask())
	for _, f := range []struct {
		on   bool
		name string
	}{
		{r.Grouped(), "grouped"},
		{r.SecondInputGrouped(), "2nd grouped"},
		{r.AssistantResetFlag(), "reset-assistants"},
		{r.Marker(), "marker"},
	} {
		if f.on {
			b.WriteString(", " + f.name)
		}
	}
	b.WriteString(")")
	return b.String()
}
