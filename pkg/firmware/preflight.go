package firmware

import (
	"errors"
	"fmt"

	"github.com/bolchisb/openveconfig/pkg/vff"
)

// Pre-flight errors. All are typed so a caller can refuse a mismatched file
// without guessing from a string.
var (
	// ErrUnsupportedTarget: the file has no main firmware version, so it
	// cannot be matched to a device.
	ErrUnsupportedTarget = errors.New("firmware: file has no main firmware version")
	// ErrFirmwareMismatch: the file is for a different device family than the
	// one connected. The original prompts for confirmation; we refuse.
	ErrFirmwareMismatch = errors.New("firmware: file does not match the device family")
	// ErrDowngrade: the file's version is older than the device's within the
	// same family (the device refuses a lower version).
	ErrDowngrade = errors.New("firmware: file is older than the firmware in the device")
	// ErrBootblockMismatch: the file requires a specific bootblock the device
	// does not have.
	ErrBootblockMismatch = errors.New("firmware: file requires a different bootblock")
	// ErrBootblockUpdateRequired: the device's bootblock is too old for the
	// file and must be updated first. Bootblock update is not implemented
	// here (#38, Unknown 1), so this refuses.
	ErrBootblockUpdateRequired = errors.New("firmware: file requires a newer bootblock than the device has (not implemented)")
)

// Target describes the connected device for pre-flight. FirmwareNumber is
// the device's current firmware number, read with the normal MK2 protocol
// (pkg/mk2.Client.SoftwareVersion). BootblockVersion and BootblockRevision
// are the device's bootblock version and revision; a negative value means
// unknown and skips the bootblock checks (the MK2 protocol does not expose
// them; they are read over the bootloader on real hardware).
type Target struct {
	FirmwareNumber    uint32
	BootblockVersion  int
	BootblockRevision int
}

// firmwareNumber returns the firmware number of the file: the Version of its
// first Main part. reverse-engineered (#12): the dispatcher reads a firmware
// number and version from the file descriptor and the device and requires
// them to match.
func firmwareNumber(f *vff.File) (int, bool) {
	for _, p := range f.Parts {
		if p.Main() && p.Meta.Has("Version") {
			return p.Meta.Int("Version"), true
		}
	}
	return 0, false
}

// mainMeta returns the metadata of the file's first Main part.
func mainMeta(f *vff.File) vff.Metadata {
	for _, p := range f.Parts {
		if p.Main() {
			return p.Meta
		}
	}
	return nil
}

// Preflight checks that f may be flashed onto t. It refuses, with a typed
// error, a file for another device family, an older file, or a file whose
// bootblock requirements the device does not meet. reverse-engineered (#12),
// spec Q8 and Q4; the guards are restated in our own words.
func Preflight(f *vff.File, t Target) error {
	num, ok := firmwareNumber(f)
	if !ok {
		return ErrUnsupportedTarget
	}
	// Family match: the family is the firmware number without its last three
	// digits (÷1000). reverse-engineered (#12).
	if num/1000 != int(t.FirmwareNumber)/1000 {
		return fmt.Errorf("%w: file firmware %d, device firmware %d", ErrFirmwareMismatch, num, t.FirmwareNumber)
	}
	// Downgrade guard: within the family the device refuses a lower version.
	if num < int(t.FirmwareNumber) {
		return fmt.Errorf("%w: file %d, device %d", ErrDowngrade, num, t.FirmwareNumber)
	}
	return bootblockGate(f, t)
}

// bootblockGate applies the file's bootblock requirements against the
// device's bootblock version. reverse-engineered (#12), spec Q4: the file
// carries MustBeBFV, MinBFV and MinBRev; a required bootblock is "not
// optional".
func bootblockGate(f *vff.File, t Target) error {
	if t.BootblockVersion < 0 {
		return nil // unknown; cannot check
	}
	m := mainMeta(f)
	if m.Has("MustBeBFV") && m.Int("MustBeBFV") != t.BootblockVersion {
		return fmt.Errorf("%w: needs bootblock %d, device has %d", ErrBootblockMismatch, m.Int("MustBeBFV"), t.BootblockVersion)
	}
	if m.Has("MinBFV") && t.BootblockVersion < m.Int("MinBFV") {
		return fmt.Errorf("%w: needs bootblock %d, device has %d", ErrBootblockUpdateRequired, m.Int("MinBFV"), t.BootblockVersion)
	}
	if m.Has("MinBRev") && t.BootblockRevision >= 0 && t.BootblockRevision < m.Int("MinBRev") {
		return fmt.Errorf("%w: needs bootblock revision %d, device has %d", ErrBootblockUpdateRequired, m.Int("MinBRev"), t.BootblockRevision)
	}
	return nil
}
