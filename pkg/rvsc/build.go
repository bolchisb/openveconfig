package rvsc

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/bolchisb/openveconfig/pkg/assistant"
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
// package already documents (unit.go): the setting-info header word 4, the
// setting-data section version 3, its 6 device-version extra bytes and the
// identify flag 1 (reverse-engineered, #7 and #56).
//
// Build is a strict superset of the backup writer. When the caller supplies
// the device-provided parts that the MK2 protocol cannot read from a bus but
// a real .rvsc download carries, Build writes them; otherwise it writes the
// structurally valid defaults it has always written. Those parts are
// (reverse-engineered, #74): the setting-info header byte8 (not constant: 2
// and 0x2f both seen), the setting-info access region and grid table after
// the records, and per unit the 6 device-version extra bytes, the response
// char and the 9-byte system record. Build also emits a minimal empty
// assistant area (the compact form of an empty image) rather than a
// zero-length area, since even a bare download carries that container. The
// identity fields a caller does not set (long ID, serial) are still left
// zero.
//
// TODO(#74): whether a real GX mk2vsc accepts a file Build makes, and the
// correct device-supplied values for byte8, the access region, the grid
// table, the device-version extra bytes and the system record, can only be
// confirmed on hardware (#3 phase 2). Build does not claim GX compatibility.

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
	buildOffIdentify     = unitOffHeader + 2                           // 0x0e: identify flag
	buildOffFirmwareNum  = unitOffHeader + 3                           // 0x0f: u32 firmware number
	buildOffExtraLen     = unitOffHeader + 7                           // 0x13: u16 device-version extra length
	buildOffVersionExtra = unitOffHeader + 9                           // 0x15: device-version extra bytes
	buildOffResponseChar = buildOffVersionExtra + buildVersionExtraLen // 0x1b: response char
	buildOffSystemRecord = buildOffResponseChar + 1                    // 0x1c: 9-byte system record
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

	// Device-provided fields a real .rvsc download carries but the MK2
	// protocol cannot read from a bus (reverse-engineered, #74). When left at
	// their zero value Build writes the all-zero defaults it always has;
	// supplying the real bytes from a download makes the file carry them.
	//
	// VersionExtra are the device-version extra bytes (Unit.VersionExtra).
	// nil means buildVersionExtraLen zero bytes; a non-nil slice must be
	// exactly buildVersionExtraLen long, because the header offsets that
	// follow assume that length.
	VersionExtra []byte
	// ResponseChar is the response char before the system record
	// (Unit.ResponseChar).
	ResponseChar byte
	// SystemRecord is the 9-byte system record (Unit.SystemRecord); its bytes
	// 6, 7 and 8 are the phase, assistant flag and unit index.
	SystemRecord [SystemRecordLen]byte
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
// reads: the 11-byte header then one 10-byte record per setting. The header
// byte8 is info.HeaderByte8 when non-zero, else the structural default
// (reverse-engineered, #74). The device-provided access region and grid table
// are appended only when the caller supplies them: the access region, a u16
// length and that many grid-table bytes (read back by GridInfo). A
// from-scratch file supplies neither and the trailer is omitted.
func encodeSettingInfo(info *SettingInfoTable) []byte {
	n := len(info.Settings)
	byte8 := info.HeaderByte8
	if byte8 == 0 {
		byte8 = infoHeaderByte8
	}
	trailer := 0
	if len(info.AccessRegion) > 0 || len(info.GridTable) > 0 {
		trailer = len(info.AccessRegion) + 2 + len(info.GridTable)
	}
	p := make([]byte, infoHeaderLen+n*infoRecordLen+trailer)
	binary.LittleEndian.PutUint32(p[0:], infoHeaderWord0)
	binary.LittleEndian.PutUint32(p[infoOffFirmware:], info.FirmwareWord)
	p[8] = byte8
	binary.LittleEndian.PutUint16(p[infoOffArrayLen:], uint16(n*infoRecordLen))
	for i, s := range info.Settings {
		r := p[infoHeaderLen+i*infoRecordLen:]
		binary.LittleEndian.PutUint16(r[0:], uint16(s.Sc))
		binary.LittleEndian.PutUint16(r[2:], uint16(s.Offset))
		binary.LittleEndian.PutUint16(r[4:], s.Default)
		binary.LittleEndian.PutUint16(r[6:], s.Min)
		binary.LittleEndian.PutUint16(r[8:], s.Max)
	}
	if trailer > 0 {
		off := infoHeaderLen + n*infoRecordLen
		off += copy(p[off:], info.AccessRegion)
		binary.LittleEndian.PutUint16(p[off:], uint16(len(info.GridTable)))
		copy(p[off+2:], info.GridTable)
	}
	return p
}

// encodeSettingData writes one BareSettingData payload in the device-form
// (not upload-form) layout newUnit reads: the identity words, a version-3
// header, the setting values and a minimal empty assistant area. The
// device-version extra bytes, response char and system record carry the
// caller's values when supplied, else zero (reverse-engineered, #74).
func encodeSettingData(u BuildUnit) ([]byte, error) {
	if len(u.Serial) > unitSerialLen {
		return nil, fmt.Errorf("serial %q is longer than %d bytes", u.Serial, unitSerialLen)
	}
	if u.VersionExtra != nil && len(u.VersionExtra) != buildVersionExtraLen {
		return nil, fmt.Errorf("version-extra must be %d bytes, got %d", buildVersionExtraLen, len(u.VersionExtra))
	}
	n := len(u.Settings)
	// The assistant area is the compact form of an empty image (reverse-
	// engineered, #74): a minimal container, not a zero-length area. #56
	// still generates no assistant code, so the image stays empty.
	area := assistant.Compact(nil)
	p := make([]byte, unitOffSettings+2*n+len(area))
	binary.LittleEndian.PutUint32(p[unitOffVersion:], buildSectionVersion)
	binary.LittleEndian.PutUint32(p[unitOffLongID:], u.LongID)
	binary.LittleEndian.PutUint32(p[unitOffFirmware:], u.FirmwareWord)
	// Header fields readHeader walks (letter and config-protocol bytes at
	// unitOffHeader are left zero).
	p[buildOffIdentify] = 1
	binary.LittleEndian.PutUint32(p[buildOffFirmwareNum:], u.FirmwareWord&0xFFFFFF)
	binary.LittleEndian.PutUint16(p[buildOffExtraLen:], buildVersionExtraLen)
	// Device-version extra bytes, response char and system record: the
	// caller's values when supplied, else left zero.
	copy(p[buildOffVersionExtra:buildOffVersionExtra+buildVersionExtraLen], u.VersionExtra)
	p[buildOffResponseChar] = u.ResponseChar
	copy(p[buildOffSystemRecord:buildOffSystemRecord+SystemRecordLen], u.SystemRecord[:])
	copy(p[unitOffSerial:unitOffSerial+unitSerialLen], u.Serial)
	// The form block (unitOffForm) stays zero: this is a device-form file.
	binary.LittleEndian.PutUint64(p[unitOffSaveTime:], uint64(u.SaveTime.Unix()))
	binary.LittleEndian.PutUint16(p[unitOffArrayLen:], uint16(2*n))
	for i, raw := range u.Settings {
		binary.LittleEndian.PutUint16(p[unitOffSettings+2*i:], raw)
	}
	copy(p[unitOffSettings+2*n:], area)
	return p, nil
}
