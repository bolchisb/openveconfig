package firmware

import (
	"context"
	"fmt"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/rvsc"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// Backing up a device's settings before flashing.
//
// Flashing resets a device to its default settings, so firmware flash (CLI)
// and the GUI wizard save the device's settings to a .rvsc file first. The
// file can be applied back with the existing "rvsc-apply" command, which
// checks it against the device before writing.

// BackupToolVersion is written into the backup file's tool-info section.
const BackupToolVersion = "openveconfig"

// BackupSettings reads every setting of cat (DefaultCatalogue if nil) from the
// device at addr through c and returns them as a one-unit rvsc.File, ready for
// MarshalBinary. The client must already be connected; BackupSettings selects
// addr itself (via vebus.ReadAll).
//
// This is a settings backup. The file holds the device's firmware number, its
// setting-info table and its setting values, which is what applying it back
// (pkg/vebus.FromRVSC, or "rvsc-apply") needs. The VE.Bus long ID, serial
// number and assistant area are left zero/empty: the MK2 protocol has no
// command that reads them from a device (see vebus.MatchRVSC). RAM variables
// are live values, not settings, and are not backed up.
func BackupSettings(ctx context.Context, c *mk2.Client, addr byte, cat *vebus.Catalogue) (*rvsc.File, error) {
	dev, _, err := vebus.ReadAll(ctx, c, addr, cat, vebus.ReadOptions{RAM: []uint16{}})
	if err != nil {
		return nil, fmt.Errorf("firmware: backup: read device %d: %w", addr, err)
	}
	return backupFile(dev)
}

// backupFile encodes a device model read from the bus as a backup file. It
// builds a dense setting array of 0..highest supported ID: settings the
// device does not support get an unsupported (Sc 0) info record and a zero
// value, so the file has one record per ID, as a real file does.
func backupFile(dev *vebus.Device) (*rvsc.File, error) {
	ids := dev.SettingIDs()
	if len(ids) == 0 {
		return nil, fmt.Errorf("firmware: backup: the device reported no settings to back up")
	}
	n := int(ids[len(ids)-1]) + 1
	info := &rvsc.SettingInfoTable{FirmwareWord: dev.Firmware, Settings: make([]mk2.SettingInfo, n)}
	values := make([]uint16, n)
	for _, id := range ids {
		si, _ := dev.SettingInfo(id)
		info.Settings[id] = si
		raw, _ := dev.Raw(vebus.SettingID(id))
		values[id] = raw
	}
	return rvsc.Build(BackupToolVersion, info, []rvsc.BuildUnit{{
		FirmwareWord: dev.Firmware,
		SaveTime:     time.Now(),
		Settings:     values,
	}})
}
