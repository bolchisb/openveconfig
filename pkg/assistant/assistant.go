// Package assistant handles a VE.Bus unit's assistant area as data: the
// image the unit stores, and the compact form in which .rvsc/.rvms files
// keep it. It does not interpret the content (#8).
//
// The compact form is a sequence of runs (reverse-engineered, #7; restated
// in our own words):
//
//	run := u16 n | n literal bytes | u8 fill | u16 count
//
// with integers little-endian. A run stands for its literal bytes followed
// by count copies of fill; the image is the runs one after another. The
// original tool turns 6 or more equal bytes into a fill run when it saves.
// Every area of a device-form file seen expands to ImageSize bytes
// (inferred: the size of the unit's assistant memory); Expand checks this
// only when asked to.
package assistant

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ImageSize is the expanded size of the assistant areas seen (inferred,
// #7).
const ImageSize = 2816

// minFill is the shortest run of equal bytes that Compact writes as a fill
// run (reverse-engineered, #7).
const minFill = 6

// ErrFormat means the compact form does not follow the run layout.
var ErrFormat = errors.New("assistant: malformed area")

// Expand returns the image of the compact form b.
func Expand(b []byte) ([]byte, error) {
	var img []byte
	for pos := 0; pos < len(b); {
		if len(b)-pos < 2 {
			return nil, fmt.Errorf("%w: %d stray byte at offset %d", ErrFormat, len(b)-pos, pos)
		}
		n := int(binary.LittleEndian.Uint16(b[pos:]))
		pos += 2
		if len(b)-pos < n+3 {
			return nil, fmt.Errorf("%w: run at offset %d ends after the area", ErrFormat, pos-2)
		}
		img = append(img, b[pos:pos+n]...)
		pos += n
		fill, count := b[pos], int(binary.LittleEndian.Uint16(b[pos+1:]))
		pos += 3
		for range count {
			img = append(img, fill)
		}
	}
	return img, nil
}

// Compact returns the compact form of img: each stretch of at least
// minFill equal bytes becomes the fill of a run, the bytes before it its
// literal; bytes after the last such stretch end in a run with no fill
// (ours: count 0, fill 0xFF). A stretch longer than a u16 count is split.
// Compact(Expand(b)) gives b back for the areas the original writes.
func Compact(img []byte) []byte {
	var out []byte
	run := func(lit []byte, fill byte, count int) {
		out = binary.LittleEndian.AppendUint16(out, uint16(len(lit)))
		out = append(out, lit...)
		out = append(out, fill)
		out = binary.LittleEndian.AppendUint16(out, uint16(count))
	}
	start := 0 // first byte of the literal being collected
	for i := 0; i < len(img); {
		j := i
		for j < len(img) && img[j] == img[i] && j-i < 0xFFFF {
			j++
		}
		if j-i < minFill {
			i = j
			continue
		}
		for start < i && i-start > 0xFFFF { // a literal longer than a u16
			run(img[start:start+0xFFFF], 0xFF, 0)
			start += 0xFFFF
		}
		run(img[start:i], img[i], j-i)
		start, i = j, j
	}
	if start < len(img) || len(img) == 0 {
		for len(img)-start > 0xFFFF {
			run(img[start:start+0xFFFF], 0xFF, 0)
			start += 0xFFFF
		}
		run(img[start:], 0xFF, 0)
	}
	return out
}
