package vebus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// Load builds a model of the device at addr by reading, through c, the
// info and value of every setting and RAM variable in cat (DefaultCatalogue
// if nil). Variables the device reports as unsupported are left out. Any
// other error stops the load; there are no retries.
//
// Load is ReadAll without retries, for tests and tools; use ReadAll for
// retries, progress, IDs outside the catalogue and a report.
func Load(ctx context.Context, c *mk2.Client, addr byte, cat *Catalogue) (*Device, error) {
	d, _, err := ReadAll(ctx, c, addr, cat, ReadOptions{Retries: -1})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// dumpFile is the part of the JSON written by "openveconfig-cli dump" that the
// model needs. Derived fields (scaled values, scale) are recomputed.
type dumpFile struct {
	Address  *uint  `json:"address"`
	Firmware uint32 `json:"firmware"`
	Settings []struct {
		ID   uint16 `json:"id"`
		Raw  uint16 `json:"raw"`
		Info struct {
			Sc          int16  `json:"sc"`
			Offset      int16  `json:"offset"`
			Default     uint16 `json:"default"`
			Min         uint16 `json:"min"`
			Max         uint16 `json:"max"`
			AccessLevel *byte  `json:"access_level"`
		} `json:"info"`
	} `json:"settings"`
	RAM []struct {
		ID   uint16 `json:"id"`
		Raw  uint16 `json:"raw"`
		Info struct {
			Sc     int16 `json:"sc"`
			Offset int16 `json:"offset"`
		} `json:"info"`
	} `json:"ram"`
}

// LoadDump builds a model from the JSON written by "openveconfig-cli dump",
// using cat (DefaultCatalogue if nil). Every setting and RAM variable in it
// is kept, including IDs not in the catalogue; its "errors" list is
// ignored.
func LoadDump(r io.Reader, cat *Catalogue) (*Device, error) {
	var f dumpFile
	if err := json.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("vebus: dump: %w", err)
	}
	if f.Address == nil || *f.Address > mk2.MaxAddress {
		return nil, errors.New("vebus: dump: missing or invalid address")
	}
	d := NewDevice(cat)
	d.Address, d.Firmware = byte(*f.Address), f.Firmware
	for _, s := range f.Settings {
		info := mk2.SettingInfo{Sc: s.Info.Sc, Offset: s.Info.Offset, Default: s.Info.Default, Min: s.Info.Min, Max: s.Info.Max}
		if s.Info.AccessLevel != nil {
			info.AccessLevel, info.HasAccessLevel = *s.Info.AccessLevel, true
		}
		d.AddSetting(s.ID, info, s.Raw)
	}
	for _, v := range f.RAM {
		d.AddRAM(v.ID, mk2.RAMInfo{Sc: v.Info.Sc, Offset: v.Info.Offset}, v.Raw)
	}
	return d, nil
}
