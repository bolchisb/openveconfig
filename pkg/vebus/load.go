package vebus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// Load builds a model of the device at addr by reading, through c, the
// info and value of every setting and RAM variable in cat (DefaultCatalogue
// if nil). Variables the device reports as unsupported are left out. Any
// other error stops the load; there are no retries.
//
// Load is a minimal reader for tests and tools. Reading and writing a whole
// device, with retries and IDs outside the catalogue, is a separate task.
func Load(ctx context.Context, c *mk2.Client, addr byte, cat *Catalogue) (*Device, error) {
	if err := c.SetAddress(ctx, addr); err != nil {
		return nil, fmt.Errorf("vebus: select address %d: %w", addr, err)
	}
	fw, err := c.SoftwareVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("vebus: firmware version: %w", err)
	}
	d := NewDevice(cat)
	d.Address, d.Firmware = addr, fw
	for _, e := range d.cat.entries {
		switch e.Kind {
		case Setting:
			info, err := c.SettingInfo(ctx, e.Num)
			if errors.Is(err, mk2.ErrNotSupported) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("vebus: setting %d: info: %w", e.Num, err)
			}
			raw, err := c.ReadSetting(ctx, e.Num)
			if errors.Is(err, mk2.ErrNotSupported) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("vebus: setting %d: read: %w", e.Num, err)
			}
			d.AddSetting(e.Num, info, raw)
		case RAM:
			if e.Num > 0xFF {
				continue
			}
			info, err := c.RAMVarInfo(ctx, e.Num)
			if errors.Is(err, mk2.ErrNotSupported) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("vebus: RAM variable %d: info: %w", e.Num, err)
			}
			vals, err := c.ReadRAMVar(ctx, byte(e.Num))
			if errors.Is(err, mk2.ErrNotSupported) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("vebus: RAM variable %d: read: %w", e.Num, err)
			}
			d.AddRAM(e.Num, info, vals[0])
		}
	}
	return d, nil
}

// dumpFile is the part of the JSON written by "veconfig-cli dump" that the
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

// LoadDump builds a model from the JSON written by "veconfig-cli dump",
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
