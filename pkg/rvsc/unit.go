package rvsc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
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
	// number (kylehart FORMAT.md §3.3).
	unitOffFirmware = 0x08
	// 0x0c..0x21: not decoded; preserved.
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

	sec      *Section
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
		Phase:          p[unitOffPhase],
		AssistantFlag:  p[unitOffAssistantFlag],
		Index:          p[unitOffIndex],
		Serial:         string(bytes.TrimRight(p[unitOffSerial:unitOffSerial+unitSerialLen], "\x00")),
		UploadForm:     !allZero(p[unitOffForm : unitOffForm+unitFormLen]),
		sec:            s,
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
// the section checksum. Its encoding is unknown (issue #7); kylehart
// describes its observed shapes in FORMAT.md §5. The package never changes
// these bytes.
func (u *Unit) AssistantArea() []byte {
	return append([]byte(nil), u.sec.Payload[u.settings+2*u.n:]...)
}
