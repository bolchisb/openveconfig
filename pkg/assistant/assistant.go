// Package assistant handles a VE.Bus unit's assistant area: the image the
// unit stores, the compact form in which .rvsc/.rvms files keep it, and
// the composer that builds an image from assistant sources (compose.go,
// #32; the sources are assembled by package asm and package wizard).
//
// The compact form is a sequence of runs (reverse-engineered, #7; restated
// in our own words):
//
//	run := u16 n | n literal bytes | u8 fill | u16 count
//
// with integers little-endian. A run stands for its literal bytes followed
// by count copies of fill; the image is the runs one after another. The
// original tool turns 6 or more equal bytes into a fill run when it saves;
// areas of LargeImageSize bytes are compacted in 64-byte blocks instead
// (see Compact). Every area of a device-form file seen expands to ImageSize bytes
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

// LargeImageSize is the expanded size of the areas of the other firmware
// family seen (observed: a GX backup of six units, firmware 1956552).
const LargeImageSize = 4096

// minFill is the shortest run of equal bytes that Compact writes as a fill
// run (reverse-engineered, #7).
const minFill = 6

// blockSize is the unit in which areas of LargeImageSize bytes are
// compacted (observed, the same backup).
const blockSize = 64

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
// literal; bytes after the last such stretch end in a run with no fill:
// count 0 and fill 0x00, as in a file the original saved (observed). A
// stretch longer than a u16 count is split.
//
// An image of LargeImageSize bytes is compacted in blocks of blockSize
// bytes: a fill run covers whole blocks only, and the last run's fill is
// 0xFF (observed: every unit of that backup; its areas hold stretches of
// up to 54 equal bytes that do not cover a whole block, kept as literal).
// Compact(Expand(b)) gives b back for the areas seen in files from a
// device and from the original.
func Compact(img []byte) []byte {
	unit, least, lastFill := 1, minFill, byte(0x00)
	if len(img) == LargeImageSize {
		unit, least, lastFill = blockSize, blockSize, 0xFF
	}
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
		// The stretch [i, j) shrunk to whole units.
		a, b := (i+unit-1)/unit*unit, j/unit*unit
		if b-a < least {
			i = j
			continue
		}
		for start < a && a-start > 0xFFFF { // a literal longer than a u16
			run(img[start:start+0xFFFF], 0xFF, 0)
			start += 0xFFFF
		}
		run(img[start:a], img[a], b-a)
		start, i = b, b
	}
	if start < len(img) || len(img) == 0 {
		for len(img)-start > 0xFFFF {
			run(img[start:start+0xFFFF], 0xFF, 0)
			start += 0xFFFF
		}
		run(img[start:], lastFill, 0)
	}
	return out
}

// Checksum returns the checksum stored in the last 4 bytes of an image: the
// sum of every other byte, inverted, each shifted by 8 × (its offset mod
// 4), minus 1 (reverse-engineered, #8). Both images checked, of one unit
// with and without assistants, carry it; the first 6 bytes of the empty
// one make it 0xFFFFFFFF, so its last 4 bytes are 0xFF like the free
// space. img must have more than 4 bytes.
func Checksum(img []byte) uint32 {
	var sum uint32
	for i, b := range img[:len(img)-4] {
		sum += uint32(b^0xFF) << (8 * (i % 4))
	}
	return sum - 1
}

// ChecksumOK reports whether the last 4 bytes of img, little-endian, are
// its Checksum.
func ChecksumOK(img []byte) bool {
	return len(img) > 4 && binary.LittleEndian.Uint32(img[len(img)-4:]) == Checksum(img)
}
