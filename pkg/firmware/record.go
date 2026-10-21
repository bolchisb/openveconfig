package firmware

import (
	"encoding/binary"

	"github.com/bolchisb/openveconfig/pkg/vff"
)

// Record is one data block to program: a 2-byte block index, a 4-byte
// address and a dataBlockLen-byte data block, with a type code naming its
// region. reverse-engineered (#12): the original builds the same index +
// address + 64-byte block from a page buffer; the exact file-to-address
// mapping is internal and not recovered, so the address and block index are
// laid out sequentially here (ours), which is enough to drive the simulator.
type Record struct {
	Index   uint16
	Address uint32
	Data    [dataBlockLen]byte
	Type    byte
}

// payload returns the record's wire payload: index, address, then the data
// block. reverse-engineered (#12).
func (r Record) payload() []byte {
	b := make([]byte, 0, 2+4+dataBlockLen)
	b = binary.LittleEndian.AppendUint16(b, r.Index)
	b = binary.LittleEndian.AppendUint32(b, r.Address)
	return append(b, r.Data[:]...)
}

// Frame returns the record's start-of-frame transmission frame.
func (r Record) Frame() ([]byte, error) { return buildFrame(r.payload()) }

// Records builds the record stream for the first group of a firmware file:
// its Main part's code blocks, and, if the group has a Bootblock part, that
// part's code blocks. Each region's bytes are the concatenation of its code
// records, split into dataBlockLen-byte blocks (the last is zero-padded).
// Block indices run across the whole stream; addresses run from 0 within
// each region (ours; see Record).
//
// A file with no Main part, or a Main part with no code, yields no records
// and is reported by the caller's pre-flight, not here.
func Records(f *vff.File) []Record {
	groups := f.Groups()
	if len(groups) == 0 {
		return nil
	}
	var recs []Record
	var idx uint16
	add := func(code [][]byte, typ byte) {
		var region []byte
		for _, r := range code {
			region = append(region, r...)
		}
		for off := 0; off < len(region); off += dataBlockLen {
			var rec Record
			rec.Index = idx
			rec.Address = uint32(off)
			rec.Type = typ
			copy(rec.Data[:], region[off:min(off+dataBlockLen, len(region))])
			recs = append(recs, rec)
			idx++
		}
	}
	for _, p := range groups[0] {
		switch {
		case p.Main():
			add(p.Code, typeMainCode)
		case p.Name() == "Bootblock":
			add(p.Code, typeBootblock)
		}
	}
	return recs
}
