package rvsc

import (
	"encoding/binary"
	"fmt"
	"time"
)

// Writing a file from scratch.
//
// Build is the inverse of Parse for the sections openveconfig writes itself: a
// tool-info section, a setting-info section and one setting-data section per
// unit. It is used to turn a device model read from the bus into a .rvsc
// backup (see pkg/firmware.BackupSettings). The byte layout is the one the
// accessors in rvsc.go and unit.go read back, so a file Build makes parses
// again; a round-trip test checks it.
//
// Constants used below are observed in the community-reported files the
// package already documents (unit.go): the setting-info header words 4 and 2,
// the setting-data section version 3, its 6 device-version extra bytes and
// the identify flag 1 (reverse-engineered, #7 and #56). A file Build makes is
// a settings backup, not a byte-for-byte copy of a device download: the
// identity fields a caller does not set (long ID, serial) and the fields the
// MK2 protocol cannot read (assistant area, system record) are left zero.

const (
	// Setting-info header words that are constant in every file seen
	// (unit.go, reverse-engineered #7).
	infoHeaderWord0 = 4
	infoHeaderByte8 = 2

	// Setting-data section version and the shape of its header for that
	// version: six device-version extra bytes, which puts the system record
	// at unitOffHeader+0x10 (0x1c), consistent with the fixed offsets
	// unitOffSerial..unitOffSettings (unit.go, reverse-engineered #7/#56).
	buildSectionVersion  = 3
	buildVersionExtraLen = 6

	// Offsets of the header fields readHeader walks for a version-3 section,
	// relative to the payload start (see Unit.readHeader).
	buildOffIdentify    = unitOffHeader + 2 // 0x0e: identify flag
	buildOffFirmwareNum = unitOffHeader + 3 // 0x0f: u32 firmware number
	buildOffExtraLen    = unitOffHeader + 7 // 0x13: u16 device-version extra length
)

// BuildUnit is the data for one setting-data section Build encodes: one unit's
// identity and setting values. Settings is indexed by setting ID and must be
// as long as the setting-info table. The caller may leave LongID and Serial
// zero for a settings-only backup.
type BuildUnit struct {
	LongID       uint32
	FirmwareWord uint32
	Serial       string
	SaveTime     time.Time
	Settings     []uint16
}

// Build makes a File with a tool-info section (omitted when toolVersion is
// ""), the setting-info table info, and one setting-data section per unit.
// Each unit's Settings must be as long as info.Settings. MarshalBinary writes
// the result.
func Build(toolVersion string, info *SettingInfoTable, units []BuildUnit) (*File, error) {
	if info == nil {
		return nil, fmt.Errorf("rvsc: Build: nil setting info")
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("rvsc: Build: no units")
	}
	f := &File{}
	if toolVersion != "" {
		f.Sections = append(f.Sections, &Section{Name: SectionToolInfo, Payload: encodeToolInfo(toolVersion)})
	}
	f.Sections = append(f.Sections, &Section{Name: SectionSettingInfo, Payload: encodeSettingInfo(info)})
	for i, u := range units {
		if len(u.Settings) != len(info.Settings) {
			return nil, fmt.Errorf("rvsc: Build: unit %d has %d settings but the setting info has %d",
				i, len(u.Settings), len(info.Settings))
		}
		p, err := encodeSettingData(u)
		if err != nil {
			return nil, fmt.Errorf("rvsc: Build: unit %d: %w", i, err)
		}
		f.Sections = append(f.Sections, &Section{Name: SectionSettingData, Payload: p})
	}
	return f, nil
}

// encodeToolInfo writes the Mk2vscInfo payload (u32 1 | u16 length | text),
// the form ToolVersion reads.
func encodeToolInfo(version string) []byte {
	p := make([]byte, 6+len(version))
	binary.LittleEndian.PutUint32(p[0:], 1)
	binary.LittleEndian.PutUint16(p[4:], uint16(len(version)))
	copy(p[6:], version)
	return p
}

// encodeSettingInfo writes the BareSettingInfo payload, the form SettingInfo
// reads: the 11-byte header then one 10-byte record per setting. No grid-info
// table is appended.
func encodeSettingInfo(info *SettingInfoTable) []byte {
	n := len(info.Settings)
	p := make([]byte, infoHeaderLen+n*infoRecordLen)
	binary.LittleEndian.PutUint32(p[0:], infoHeaderWord0)
	binary.LittleEndian.PutUint32(p[infoOffFirmware:], info.FirmwareWord)
	p[8] = infoHeaderByte8
	binary.LittleEndian.PutUint16(p[infoOffArrayLen:], uint16(n*infoRecordLen))
	for i, s := range info.Settings {
		r := p[infoHeaderLen+i*infoRecordLen:]
		binary.LittleEndian.PutUint16(r[0:], uint16(s.Sc))
		binary.LittleEndian.PutUint16(r[2:], uint16(s.Offset))
		binary.LittleEndian.PutUint16(r[4:], s.Default)
		binary.LittleEndian.PutUint16(r[6:], s.Min)
		binary.LittleEndian.PutUint16(r[8:], s.Max)
	}
	return p
}

// encodeSettingData writes one BareSettingData payload in the device-form
// (not upload-form) layout newUnit reads: the identity words, a version-3
// header, the setting values and no assistant area.
func encodeSettingData(u BuildUnit) ([]byte, error) {
	if len(u.Serial) > unitSerialLen {
		return nil, fmt.Errorf("serial %q is longer than %d bytes", u.Serial, unitSerialLen)
	}
	n := len(u.Settings)
	p := make([]byte, unitOffSettings+2*n)
	binary.LittleEndian.PutUint32(p[unitOffVersion:], buildSectionVersion)
	binary.LittleEndian.PutUint32(p[unitOffLongID:], u.LongID)
	binary.LittleEndian.PutUint32(p[unitOffFirmware:], u.FirmwareWord)
	// Header fields readHeader walks (letter and config-protocol bytes at
	// unitOffHeader are left zero).
	p[buildOffIdentify] = 1
	binary.LittleEndian.PutUint32(p[buildOffFirmwareNum:], u.FirmwareWord&0xFFFFFF)
	binary.LittleEndian.PutUint16(p[buildOffExtraLen:], buildVersionExtraLen)
	// Device-version extra bytes, response char and the 9-byte system record
	// are left zero.
	copy(p[unitOffSerial:unitOffSerial+unitSerialLen], u.Serial)
	// The form block (unitOffForm) stays zero: this is a device-form file.
	binary.LittleEndian.PutUint64(p[unitOffSaveTime:], uint64(u.SaveTime.Unix()))
	binary.LittleEndian.PutUint16(p[unitOffArrayLen:], uint16(2*n))
	for i, raw := range u.Settings {
		binary.LittleEndian.PutUint16(p[unitOffSettings+2*i:], raw)
	}
	return p, nil
}
