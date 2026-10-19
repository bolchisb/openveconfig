// Package rvsc reads and writes the .rvsc/.rvms settings files that a GX
// device's mk2vsc program and VRM Remote VEConfigure produce and consume.
//
// Victron has published no specification of this format. Everything here is
// community-reported unless marked otherwise:
//
//   - kylehart/mk2vsc (MIT), docs/FORMAT.md §1 "File grammar", §1.1 "The
//     checksum", §2, §3.1 and §4, and mk2vsc/sections.py, mk2vsc/units.py
//     and mk2vsc/schema.py: https://github.com/kylehart/mk2vsc
//   - talas9/rvsc-tools (MIT), FORMAT.md §4 "Container Format" and §5.1
//     "BareSettingInfo": https://github.com/talas9/rvsc-tools
//
// .rvsc and .rvms are the same container; a .rvms holds one BareSettingData
// section per unit (kylehart FORMAT.md, "Names").
//
// The container grammar, the checksum and the unit block offsets are adapted
// from kylehart/mk2vsc (MIT, Copyright (c) 2026 Kyle Hart); see
// THIRD_PARTY_NOTICES.md.
//
// Layout (all integers little-endian):
//
//	file    := u16 29 | "VEConfig setting section file" | section*
//	section := u16 nameLen | name | u32 next | payload | u32 checksum
//
// next is the absolute file offset of the following section (the file length
// for the last one). checksum is the sum, modulo 2^32, of the section's bytes
// from its u16 nameLen up to the checksum, read as little-endian 32-bit words,
// a trailing partial word zero-padded on the high side.
//
// The package keeps every section as a name and an opaque payload, so a file
// it parses is written back byte for byte, unknown sections included. The
// typed accessors (ToolVersion, SettingInfo, Units) read the known sections;
// Unit.SetSetting edits one setting value in place and leaves every other
// byte alone. Checksums and next pointers are always computed when the file
// is written, so an edited file is consistent by construction.
package rvsc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Magic is the name of the header that opens every file. The header is a u16
// length and this text, with no pointer, payload or checksum.
const Magic = "VEConfig setting section file"

// Known section names.
const (
	SectionToolInfo    = "Mk2vscInfo"      // mk2vsc version
	SectionSettingInfo = "BareSettingInfo" // per-setting scale, offset, default, min, max
	SectionSettingData = "BareSettingData" // one per unit: identity, setting values, assistant area
)

// Errors returned by this package wrap one of these.
var (
	// ErrFormat means the bytes do not follow the expected layout.
	ErrFormat = errors.New("rvsc: malformed file")
	// ErrChecksum means a section's stored checksum does not match its
	// bytes. mk2vsc reports this as error 53 "file checksum error" (Venus
	// wiki, dbus page, "VE.Bus Backup&Restore").
	ErrChecksum = errors.New("rvsc: section checksum mismatch")
)

const headerLen = 2 + len(Magic)

// Section is one named section. Payload is everything between the next
// pointer and the checksum; the pointer and the checksum are computed on
// write.
type Section struct {
	Name    string
	Payload []byte
}

// File is a parsed .rvsc or .rvms file: its sections in file order.
type File struct {
	Sections []*Section
}

// Parse decodes a file. It checks the header, the pointer chain (every
// section must start where the previous one ends and the last must end at
// the end of data) and every section checksum. It does not interpret the
// payloads; see ToolVersion, SettingInfo and Units. Parse copies data, so
// later edits to the File do not touch the caller's slice.
func Parse(data []byte) (*File, error) {
	data = append([]byte(nil), data...)
	if len(data) < headerLen || binary.LittleEndian.Uint16(data) != uint16(len(Magic)) ||
		string(data[2:headerLen]) != Magic {
		return nil, fmt.Errorf("%w: missing %q header", ErrFormat, Magic)
	}
	f := &File{}
	for pos := headerLen; pos < len(data); {
		if len(data)-pos < 2 {
			return nil, fmt.Errorf("%w: truncated section at %#x", ErrFormat, pos)
		}
		nameLen := int(binary.LittleEndian.Uint16(data[pos:]))
		ptrAt := pos + 2 + nameLen
		if ptrAt+4 > len(data) {
			return nil, fmt.Errorf("%w: truncated section at %#x", ErrFormat, pos)
		}
		name := string(data[pos+2 : ptrAt])
		if !validName(name) {
			return nil, fmt.Errorf("%w: implausible section name %q at %#x", ErrFormat, name, pos)
		}
		next := uint64(binary.LittleEndian.Uint32(data[ptrAt:]))
		if next < uint64(ptrAt+8) || next > uint64(len(data)) {
			return nil, fmt.Errorf("%w: section %q at %#x points to %#x", ErrFormat, name, pos, next)
		}
		end := int(next)
		stored := binary.LittleEndian.Uint32(data[end-4:])
		if sum := checksum(data[pos : end-4]); sum != stored {
			return nil, fmt.Errorf("%w: section %d %q at %#x stores %08x, computed %08x",
				ErrChecksum, len(f.Sections), name, pos, stored, sum)
		}
		f.Sections = append(f.Sections, &Section{Name: name, Payload: data[ptrAt+4 : end-4]})
		pos = end
	}
	return f, nil
}

// MarshalBinary writes the file, computing every next pointer and checksum.
// For a file returned by Parse and not edited since, the result equals the
// parsed bytes.
func (f *File) MarshalBinary() ([]byte, error) {
	size := headerLen
	for i, s := range f.Sections {
		if !validName(s.Name) || len(s.Name) > math.MaxUint16 {
			return nil, fmt.Errorf("%w: section %d has invalid name %q", ErrFormat, i, s.Name)
		}
		size += 2 + len(s.Name) + 4 + len(s.Payload) + 4
		if uint64(size) > math.MaxUint32 {
			return nil, fmt.Errorf("%w: file larger than 4 GiB", ErrFormat)
		}
	}
	out := make([]byte, 0, size)
	out = binary.LittleEndian.AppendUint16(out, uint16(len(Magic)))
	out = append(out, Magic...)
	for _, s := range f.Sections {
		start := len(out)
		next := start + 2 + len(s.Name) + 4 + len(s.Payload) + 4
		out = binary.LittleEndian.AppendUint16(out, uint16(len(s.Name)))
		out = append(out, s.Name...)
		out = binary.LittleEndian.AppendUint32(out, uint32(next))
		out = append(out, s.Payload...)
		out = binary.LittleEndian.AppendUint32(out, checksum(out[start:]))
	}
	return out, nil
}

// validName accepts printable ASCII without spaces. It catches a broken
// pointer chain early, as kylehart/mk2vsc's parser does.
func validName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x21 || name[i] > 0x7e {
			return false
		}
	}
	return true
}

// checksum is the section checksum: the sum of b as little-endian 32-bit
// words modulo 2^32, a trailing partial word zero-padded on the high side
// (kylehart/mk2vsc docs/FORMAT.md §1.1, sum32_le in mk2vsc/sections.py).
func checksum(b []byte) uint32 {
	var sum uint32
	for len(b) >= 4 {
		sum += binary.LittleEndian.Uint32(b)
		b = b[4:]
	}
	var last [4]byte
	copy(last[:], b)
	return sum + binary.LittleEndian.Uint32(last[:])
}

// section returns the only section called name.
func (f *File) section(name string) (*Section, error) {
	var found *Section
	for _, s := range f.Sections {
		if s.Name == name {
			if found != nil {
				return nil, fmt.Errorf("%w: more than one %s section", ErrFormat, name)
			}
			found = s
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: no %s section", ErrFormat, name)
	}
	return found, nil
}

// ToolVersion returns the version text in the Mk2vscInfo section, for
// example "1.33": the version of the GX-side mk2vsc program (kylehart
// FORMAT.md §2). The payload is u32 1 | u16 length | text.
func (f *File) ToolVersion() (string, error) {
	s, err := f.section(SectionToolInfo)
	if err != nil {
		return "", err
	}
	p := s.Payload
	if len(p) < 6 || len(p)-6 < int(binary.LittleEndian.Uint16(p[4:])) {
		return "", fmt.Errorf("%w: %s payload too short", ErrFormat, SectionToolInfo)
	}
	return string(p[6 : 6+int(binary.LittleEndian.Uint16(p[4:]))]), nil
}
