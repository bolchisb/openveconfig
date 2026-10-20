// Package rvsctest builds synthetic .rvsc/.rvms files for tests.
//
// Every value is supplied by the test; nothing comes from a real file. The
// layout follows the community-reported format that pkg/rvsc documents
// (kylehart/mk2vsc FORMAT.md, MIT); bytes whose meaning is unknown are
// filled with an invented pattern. The builder computes checksums with its
// own code, so tests do not only check pkg/rvsc against itself.
package rvsctest

import (
	"encoding/binary"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// Unit describes one BareSettingData section, in device form.
type Unit struct {
	LongID, Firmware            uint32
	Phase, AssistantFlag, Index byte
	Serial                      string // at most 11 bytes
	SaveTime                    uint32 // Unix seconds
	Settings                    []uint16
	Assistant                   []byte
}

// File describes a whole file.
type File struct {
	ToolVersion  string
	InfoFirmware uint32
	Info         []mk2.SettingInfo
	InfoTail     []byte // bytes after the info records
	Units        []Unit
}

// Bytes assembles the file.
func (f File) Bytes() []byte {
	b := binary.LittleEndian.AppendUint16(nil, 29)
	b = append(b, "VEConfig setting section file"...)
	b = section(b, "Mk2vscInfo", toolInfo(f.ToolVersion))
	b = section(b, "BareSettingInfo", settingInfo(f.InfoFirmware, f.Info, f.InfoTail))
	for _, u := range f.Units {
		b = section(b, "BareSettingData", unit(u))
	}
	return b
}

func section(b []byte, name string, payload []byte) []byte {
	start := len(b)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(name)))
	b = append(b, name...)
	b = binary.LittleEndian.AppendUint32(b, uint32(start+2+len(name)+4+len(payload)+4))
	b = append(b, payload...)
	var sum uint32
	for i, c := range b[start:] {
		sum += uint32(c) << (8 * (i % 4))
	}
	return binary.LittleEndian.AppendUint32(b, sum)
}

func toolInfo(v string) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 1)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(v)))
	return append(b, v...)
}

func settingInfo(fw uint32, recs []mk2.SettingInfo, tail []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 4)
	b = binary.LittleEndian.AppendUint32(b, fw)
	b = append(b, 2)
	b = binary.LittleEndian.AppendUint16(b, uint16(10*len(recs)))
	for _, r := range recs {
		b = binary.LittleEndian.AppendUint16(b, uint16(r.Sc))
		b = binary.LittleEndian.AppendUint16(b, uint16(r.Offset))
		b = binary.LittleEndian.AppendUint16(b, r.Default)
		b = binary.LittleEndian.AppendUint16(b, r.Min)
		b = binary.LittleEndian.AppendUint16(b, r.Max)
	}
	return append(b, tail...)
}

func unit(u Unit) []byte {
	b := binary.LittleEndian.AppendUint32(nil, 3)
	b = binary.LittleEndian.AppendUint32(b, u.LongID)
	b = binary.LittleEndian.AppendUint32(b, u.Firmware)
	for i := 0x0c; i < 0x22; i++ { // undecoded bytes: an invented pattern
		b = append(b, byte(0x40+i))
	}
	binary.LittleEndian.PutUint16(b[0x13:], 6) // 6 extra bytes: the system record starts at 0x1c
	b = append(b, u.Phase, u.AssistantFlag, u.Index)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(u.Serial)))
	serial := make([]byte, 11)
	copy(serial, u.Serial)
	b = append(b, serial...)
	b = append(b, make([]byte, 10)...) // device form
	b = binary.LittleEndian.AppendUint64(b, uint64(u.SaveTime))
	b = binary.LittleEndian.AppendUint16(b, uint16(2*len(u.Settings)))
	for _, v := range u.Settings {
		b = binary.LittleEndian.AppendUint16(b, v)
	}
	return append(b, u.Assistant...)
}

// FromProfile returns a one-unit file whose setting info and values (the
// defaults) are those of a simulator profile, for n setting IDs. Settings
// the profile lacks get Sc 0 (unsupported) and value 0.
func FromProfile(p simulator.Profile, n int, longID uint32, serial string) File {
	info := make([]mk2.SettingInfo, n)
	vals := make([]uint16, n)
	for _, s := range p.Settings {
		if int(s.ID) >= n {
			continue
		}
		info[s.ID] = mk2.SettingInfo{Sc: s.Sc, Offset: s.Offset, Default: s.Default, Min: s.Min, Max: s.Max}
		vals[s.ID] = s.Default
	}
	return File{
		ToolVersion:  "0.01",
		InfoFirmware: p.Version,
		Info:         info,
		InfoTail:     []byte{0x13, 0x37},
		Units: []Unit{{
			LongID: longID, Firmware: p.Version, AssistantFlag: 0xf0,
			Serial: serial, SaveTime: 1700000000, Settings: vals,
			Assistant: []byte{0x00, 0x00, 0xff, 0x00, 0x0b},
		}},
	}
}
