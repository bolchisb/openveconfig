package rvsc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// BareSettingInfo payload layout (kylehart FORMAT.md §2, mk2vsc/schema.py):
// an 11-byte header, then one 10-byte record per setting ID, then bytes
// nobody has decoded yet (kept as they are). The header is u32 (4 in every
// file seen) | u32 firmware word | u8 (2 in every file seen) | u16.
//
// INFERRED (ours): the final header u16 is the byte length of the record
// array. Every file kylehart reports has 0x0780 there and 192 records of 10
// bytes (1920 = 0x0780). Likewise the u16 just before a unit's settings
// array reads 0x0180 = 2 × 192. Both are used here to size the arrays and are
// checked, so a file where the guess is wrong fails with ErrFormat instead
// of being misread.
const (
	infoOffFirmware = 4
	infoOffArrayLen = 9
	infoHeaderLen   = 11
	infoRecordLen   = 10
)

// BareSettingData payload layout. Offsets are from the start of the payload
// (kylehart's offsets are from the section name, 0x13 bytes earlier).
// Sources: kylehart FORMAT.md §3.1 and §4, mk2vsc/units.py. Where the
// behavioural spec of issue #7 adds meaning, it says "reverse-engineered
// (#7)".
const (
	// u32, 3 in every block seen. Reverse-engineered (#7): a section
	// version; with version 3 or later the block carries more fields.
	unitOffVersion = 0x00
	// u32, constant per unit. Reverse-engineered (#7): the unit's VE.Bus
	// long ID.
	unitOffLongID = 0x04
	// u32 firmware word; the low 24 bits are the seven-digit firmware
	// number (kylehart FORMAT.md §3.3). Reverse-engineered (#56): the
	// unit's version word; a high byte of 0xc7 marks an external BF.
	unitOffFirmware = 0x08
	// 0x0c..0x24: read sequentially by readHeader, up to and including
	// the system record. In a section of version 3 whose device-version
	// extra bytes are 6 long, the record is at 0x1c..0x24 and its bytes
	// 6, 7 and 8 are at the three offsets below (community, kylehart
	// FORMAT.md §3.1.1). Phase, AssistantFlag and Index are read from the
	// record wherever readHeader finds it.
	unitOffHeader        = 0x0c
	unitOffPhase         = 0x22 // 00 L1, 04 L2, 08 L3, 86 split-phase second leg (community, inferred)
	unitOffAssistantFlag = 0x23 // high nibble e: configured for an assistant; f: bare
	unitOffIndex         = 0x24 // unit index in the system
	unitOffSerial        = 0x27 // ASCII serial, zero padded
	unitSerialLen        = 11
	// 10 zero bytes in a file downloaded from a device; a file exported by
	// VEConfigure for upload has a 16-byte blob and 4 zero bytes here, which
	// moves everything after it by unitUploadShift.
	unitOffForm     = 0x32
	unitFormLen     = 10
	unitUploadShift = 10
	// Device form: file generation time, Unix seconds. Read as 8 bytes:
	// kylehart sees a u32 followed by a zero u32; reverse-engineered (#7):
	// an 8-byte timestamp.
	unitOffSaveTime = 0x3c
	unitOffArrayLen = 0x44 // u16 byte length of the settings array (inferred, see above)
	unitOffSettings = 0x46 // u16 per setting ID, then the assistant area
)

// SettingInfoTable is the content of the BareSettingInfo section: the
// device's own setting metadata, as CommandGetSettingInfo returns it (MK2
// protocol 3.14 §7.3.8). Settings[id] is setting ID id. AccessLevel is not
// in the file; HasAccessLevel is false.
type SettingInfoTable struct {
	// FirmwareWord is the u32 in the section header. kylehart reports that
	// its low 24 bits are a firmware number that can differ from the
	// units' (FORMAT.md §3.3).
	FirmwareWord uint32
	Settings     []mk2.SettingInfo
}

// SettingInfo decodes the BareSettingInfo section. The bytes after the
// records are not decoded; they stay in the section payload unchanged.
func (f *File) SettingInfo() (*SettingInfoTable, error) {
	s, err := f.section(SectionSettingInfo)
	if err != nil {
		return nil, err
	}
	p := s.Payload
	if len(p) < infoHeaderLen {
		return nil, fmt.Errorf("%w: %s payload too short", ErrFormat, SectionSettingInfo)
	}
	n := int(binary.LittleEndian.Uint16(p[infoOffArrayLen:]))
	if n%infoRecordLen != 0 || len(p)-infoHeaderLen < n {
		return nil, fmt.Errorf("%w: %s record array length %d does not fit", ErrFormat, SectionSettingInfo, n)
	}
	t := &SettingInfoTable{
		FirmwareWord: binary.LittleEndian.Uint32(p[infoOffFirmware:]),
		Settings:     make([]mk2.SettingInfo, n/infoRecordLen),
	}
	for i := range t.Settings {
		r := p[infoHeaderLen+i*infoRecordLen:]
		t.Settings[i] = mk2.SettingInfo{
			Sc:      int16(binary.LittleEndian.Uint16(r[0:])),
			Offset:  int16(binary.LittleEndian.Uint16(r[2:])),
			Default: binary.LittleEndian.Uint16(r[4:]),
			Min:     binary.LittleEndian.Uint16(r[6:]),
			Max:     binary.LittleEndian.Uint16(r[8:]),
		}
	}
	return t, nil
}

// GridInfo returns a copy of the grid-info table stored at the end of the
// BareSettingInfo section, or nil when the section holds none. The table
// carries, per grid code, the parameter-set version that the grid checker
// words use (pkg/gridcode.ParamSetVersion).
//
// Reverse-engineered (#27), layout INFERRED: after the setting records
// come bytes not decoded, one access byte per setting, a u16 length L and
// the L-byte table, which runs to the end of the section. Since the length
// of the undecoded bytes is not known, the table is found as the one place
// after room for the access bytes where a u16 equals the number of bytes
// after it, at least the table's 5-byte header. More than one such place
// is an ErrFormat error.
func (f *File) GridInfo() ([]byte, error) {
	t, err := f.SettingInfo()
	if err != nil {
		return nil, err
	}
	s, _ := f.section(SectionSettingInfo)
	tail := s.Payload[infoHeaderLen+infoRecordLen*len(t.Settings):]
	found := -1
	for i := len(t.Settings); i+2 <= len(tail); i++ {
		if l := int(binary.LittleEndian.Uint16(tail[i:])); l >= gridInfoMinLen && l == len(tail)-i-2 {
			if found >= 0 {
				return nil, fmt.Errorf("%w: %s holds more than one possible grid-info table", ErrFormat, SectionSettingInfo)
			}
			found = i
		}
	}
	if found < 0 {
		return nil, nil
	}
	return append([]byte(nil), tail[found+2:]...), nil
}

// gridInfoMinLen is the length of the grid-info table's header.
const gridInfoMinLen = 5

// Unit is one BareSettingData section: one VE.Bus unit's identity, setting
// values and assistant area. The identity fields are read when Units is
// called; changing them does not change the file. Setting values are read
// from and written to the section payload directly; call Units again after
// replacing a section's Payload.
type Unit struct {
	SectionVersion uint32
	LongID         uint32
	FirmwareWord   uint32
	Phase          byte
	AssistantFlag  byte
	Index          byte
	Serial         string
	// UploadForm is true for a file exported by VEConfigure for upload,
	// false for one downloaded from a device (kylehart FORMAT.md §4).
	UploadForm bool
	// SaveTime is when the file was generated. kylehart reports that a
	// device accepted files with older times (FORMAT.md §3.2); mk2vsc has
	// a "file expired" error (58) whose rule is unknown.
	SaveTime time.Time
	// Reverse-engineered (#56): the letter index and config-protocol byte
	// (0xff and 0 in sections older than version 3), the identify flag
	// (1 normally) and the response char that precede the system record.
	Letter         byte
	ConfigProtocol byte
	IdentifyFlag   byte
	ResponseChar   byte
	// VersionExtra are the device-version extra bytes (reverse-engineered,
	// #56; see gridcode.InternalProtection for one of their bits).
	VersionExtra []byte

	sec      *Section
	record   int // payload offset of the system record
	settings int // payload offset of setting 0
	n        int // number of settings
}

// Units decodes every BareSettingData section, in file order. Block order is
// not stable between downloads of the same system (kylehart FORMAT.md §3.1);
// match units by Serial or LongID.
func (f *File) Units() ([]*Unit, error) {
	var units []*Unit
	for i, s := range f.Sections {
		if s.Name != SectionSettingData {
			continue
		}
		u, err := newUnit(s)
		if err != nil {
			return nil, fmt.Errorf("%w (section %d)", err, i)
		}
		units = append(units, u)
	}
	return units, nil
}

func newUnit(s *Section) (*Unit, error) {
	p := s.Payload
	if len(p) < unitOffSettings {
		return nil, fmt.Errorf("%w: %s payload too short", ErrFormat, SectionSettingData)
	}
	u := &Unit{
		SectionVersion: binary.LittleEndian.Uint32(p[unitOffVersion:]),
		LongID:         binary.LittleEndian.Uint32(p[unitOffLongID:]),
		FirmwareWord:   binary.LittleEndian.Uint32(p[unitOffFirmware:]),
		Serial:         string(bytes.TrimRight(p[unitOffSerial:unitOffSerial+unitSerialLen], "\x00")),
		UploadForm:     !allZero(p[unitOffForm : unitOffForm+unitFormLen]),
		sec:            s,
	}
	if err := u.readHeader(p); err != nil {
		return nil, err
	}
	shift := 0
	if u.UploadForm {
		shift = unitUploadShift
	}
	u.settings = unitOffSettings + shift
	if len(p) < u.settings {
		return nil, fmt.Errorf("%w: %s payload too short", ErrFormat, SectionSettingData)
	}
	n := int(binary.LittleEndian.Uint16(p[unitOffArrayLen+shift:]))
	if n%2 != 0 || len(p)-u.settings < n {
		return nil, fmt.Errorf("%w: %s settings array length %d does not fit", ErrFormat, SectionSettingData, n)
	}
	u.n = n / 2
	u.SaveTime = time.Unix(int64(binary.LittleEndian.Uint64(p[unitOffSaveTime+shift:])), 0).UTC()
	return u, nil
}

// SystemRecordLen is the length of a unit's system record.
const SystemRecordLen = 9

// readHeader reads the fields between the version word and the system
// record, sequentially. Reverse-engineered (#56): from 0x0c the payload
// holds, in order, a u8 letter index and a u8 config-protocol byte (section
// version 3 and later only), a u8 identify flag, a u32 firmware number, a
// u16 length and that many device-version extra bytes, a u8 response char
// and the 9-byte system record. Section versions 1 to 3 exist.
func (u *Unit) readHeader(p []byte) error {
	if u.SectionVersion < 1 || u.SectionVersion > 3 {
		return fmt.Errorf("%w: %s section version %d is not supported", ErrFormat, SectionSettingData, u.SectionVersion)
	}
	pos := unitOffHeader
	u.Letter, u.ConfigProtocol = 0xff, 0
	if u.SectionVersion >= 3 {
		u.Letter, u.ConfigProtocol = p[pos], p[pos+1]
		pos += 2
	}
	u.IdentifyFlag = p[pos]
	pos += 1 + 4 // identify flag, firmware number
	extra := pos + 2
	pos += 2 + int(binary.LittleEndian.Uint16(p[pos:]))
	if pos+1+SystemRecordLen > len(p) {
		return fmt.Errorf("%w: %s system record beyond the payload", ErrFormat, SectionSettingData)
	}
	u.VersionExtra = append([]byte(nil), p[extra:pos]...)
	u.ResponseChar = p[pos]
	u.record = pos + 1
	rec := p[u.record:]
	u.Phase, u.AssistantFlag, u.Index = rec[6], rec[7], rec[8]
	return nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// Firmware returns the firmware number: the low 24 bits of FirmwareWord.
// kylehart saw the high byte set (0xc7) on one file and reads it as not part
// of the number (FORMAT.md §3.3, inferred from one file).
func (u *Unit) Firmware() uint32 { return u.FirmwareWord & 0xFFFFFF }

// ExternalBF reports whether the version word marks the unit as having an
// external BF: its high byte is 0xc7 (reverse-engineered, #56).
func (u *Unit) ExternalBF() bool { return u.FirmwareWord>>24 == 0xc7 }

// SystemRecord returns the unit's 9-byte system record, undecoded.
func (u *Unit) SystemRecord() [SystemRecordLen]byte {
	var r [SystemRecordLen]byte
	copy(r[:], u.sec.Payload[u.record:])
	return r
}

// SetSystemRecord stores r as the unit's system record and updates Phase,
// AssistantFlag and Index (its bytes 6, 7 and 8). Only those 9 bytes of the
// section change; the section checksum follows when the file is written.
func (u *Unit) SetSystemRecord(r [SystemRecordLen]byte) {
	copy(u.sec.Payload[u.record:], r[:])
	u.Phase, u.AssistantFlag, u.Index = r[6], r[7], r[8]
}

// NumSettings returns the number of setting values in the unit; setting IDs
// are 0..NumSettings()-1.
func (u *Unit) NumSettings() int { return u.n }

// Setting returns the raw value of setting ID id.
func (u *Unit) Setting(id int) (uint16, error) {
	if id < 0 || id >= u.n {
		return 0, fmt.Errorf("rvsc: setting %d out of range 0..%d", id, u.n-1)
	}
	return binary.LittleEndian.Uint16(u.sec.Payload[u.settings+2*id:]), nil
}

// Settings returns the raw values of all settings, indexed by setting ID.
func (u *Unit) Settings() []uint16 {
	v := make([]uint16, u.n)
	for i := range v {
		v[i] = binary.LittleEndian.Uint16(u.sec.Payload[u.settings+2*i:])
	}
	return v
}

// SetSetting stores raw as the value of setting ID id. Only those two bytes
// of the section change; the section checksum follows when the file is
// written. It does not check the value against the setting's Min and Max.
func (u *Unit) SetSetting(id int, raw uint16) error {
	if id < 0 || id >= u.n {
		return fmt.Errorf("rvsc: setting %d out of range 0..%d", id, u.n-1)
	}
	binary.LittleEndian.PutUint16(u.sec.Payload[u.settings+2*id:], raw)
	return nil
}

// AssistantArea returns a copy of the bytes between the settings array and
// the section checksum: the unit's assistant area in the compact form of
// package assistant (reverse-engineered, #7; kylehart describes its
// observed shapes in FORMAT.md §5). Only SetAssistantImage changes them.
func (u *Unit) AssistantArea() []byte {
	return append([]byte(nil), u.sec.Payload[u.settings+2*u.n:]...)
}

// AssistantImage returns the unit's assistant area expanded
// (assistant.Expand).
func (u *Unit) AssistantImage() ([]byte, error) {
	return assistant.Expand(u.sec.Payload[u.settings+2*u.n:])
}

// SetAssistantImage replaces the unit's assistant area with the compact
// form of img (assistant.Compact). The section's length, pointers and
// checksum follow when the file is written.
func (u *Unit) SetAssistantImage(img []byte) {
	end := u.settings + 2*u.n
	u.sec.Payload = append(u.sec.Payload[:end:end], assistant.Compact(img)...)
}
