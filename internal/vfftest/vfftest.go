// Package vfftest builds synthetic .vff firmware files for tests.
//
// Every value is supplied by the test or invented; nothing comes from a
// real firmware file (#2). The layout follows pkg/vff's description of the
// container, assembled with this package's own code so that tests do not
// only check pkg/vff against itself.
package vfftest

import "encoding/binary"

// Magic is the text of the magic record.
const Magic = "Victron Energy firmware file"

// BlockLen is the length of the data blocks of formats 1 and 2.
const BlockLen = 74

// Encode returns plain data in the encoded form.
func Encode(plain []byte) []byte {
	out := make([]byte, len(plain))
	for i, c := range plain {
		out[i] = c + byte(0x0C+i)
	}
	return out
}

// Check returns the check byte of plain data.
func Check(plain []byte) byte {
	var s byte
	for _, c := range plain {
		s += c
	}
	return s
}

// Rec appends a record of plain data, with a u16 (wide) or u8 length,
// stored encoded or as it is.
func Rec(b []byte, plain []byte, wide, encoded bool) []byte {
	if wide {
		b = binary.LittleEndian.AppendUint16(b, uint16(len(plain)))
	} else {
		b = append(b, byte(len(plain)))
	}
	stored := plain
	if encoded {
		stored = Encode(plain)
	}
	return append(append(b, stored...), Check(plain))
}

// Header returns a part header: the magic record and the format.
func Header(format int, encodedMagic bool) []byte {
	b := Rec(nil, []byte(Magic), false, encodedMagic)
	return binary.LittleEndian.AppendUint16(b, uint16(format))
}

// Blob returns n invented bytes.
func Blob(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(7*i)
	}
	return b
}

// Spec3 describes a part of format 3-7.
type Spec3 struct {
	Format     int
	Meta       string
	Desc       []byte
	ML, EL, CL [][]byte
}

// Part3 builds a part of format 3-7 with its checksum.
func Part3(s Spec3) []byte {
	b := Header(s.Format, false)
	at := len(b)
	b = append(b, 0, 0, 0, 0)
	b = Rec(b, []byte(s.Meta), true, true)
	b = Rec(b, s.Desc, true, true)
	for _, r := range s.ML {
		b = Rec(b, r, true, true)
	}
	for _, r := range append(append([][]byte{}, s.EL...), s.CL...) {
		b = Rec(b, r, true, false)
	}
	var sum uint32
	for _, c := range b {
		sum += uint32(c)
	}
	binary.LittleEndian.PutUint32(b[at:], sum)
	return b
}

// Records returns n invented records of size bytes.
func Records(n, size int, seed byte) [][]byte {
	var rs [][]byte
	for i := range n {
		rs = append(rs, Blob(size, seed+byte(i)))
	}
	return rs
}

// File7 is a format-7 file: an inverter Main part, its Bootblock and a
// second Main part.
func File7() []byte {
	b := Part3(Spec3{7, `Part=Main,Version=2623457,"Device=Multi Test 12V",EL=2,CL=3,ConnectInfo=aP18K,Nonce=00001234,KeyID=3,Foo=bar`,
		append([]byte("C"), Blob(32, 1)...), nil, Records(2, 73, 10), Records(3, 73, 20)})
	b = append(b, Part3(Spec3{7, "Part=Bootblock,Version=115,CL=1,IDOfBootBlockMerged=00000021", []byte("C"), nil, nil, Records(1, 73, 30)})...)
	return append(b, Part3(Spec3{7, `Part=Main,Version=2623457,"Device=Connection board",EL=1,CL=1,ConnectInfo=aCNB`, []byte("C"), nil, Records(1, 73, 40), Records(1, 73, 50)})...)
}

// File1 is a format-1 file: encoded magic, no metadata, blocks to the end.
func File1(blocks int) []byte {
	b := Header(1, true)
	var data []byte
	for i := range blocks {
		data = append(data, Blob(BlockLen, byte(i))...)
	}
	var sum uint32
	for _, c := range data {
		sum += uint32(c)
	}
	b = binary.LittleEndian.AppendUint32(b, sum)
	b = Rec(b, Blob(38, 3), false, true)
	b = Rec(b, []byte("line"), false, true)
	b = Rec(b, nil, false, true)
	return append(b, data...)
}
