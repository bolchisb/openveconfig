package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
	"github.com/bolchisb/victron-veconfigure-ported/pkg/serialport"
)

// probeTimeout is how long ports --probe waits for the 'V' reply. It is
// longer than mk2.DefaultTimeout because an RS232 MK2.2 takes its power
// from DTR, which is only raised when the port is opened.
const probeTimeout = time.Second

var portsCmd = &command{
	name:    "ports",
	summary: "list serial ports and flag likely MK2/MK3 interfaces",
	help: `A port is flagged by its USB identity: FTDI FT-X 0403:6015 (MK3-USB) or
Prolific PL2303 067B:2303 (USB-RS232 converter for the RS232 MK2.2). The
MK2-USB has no known identity, and an FTDI ID is shared by many other
adapters, so a flag is only a hint.

--probe opens every port at 2400 baud and sends one MK2 version request
('V'). Only use it when no other program needs the ports and nothing but
MK2/MK3 interfaces could react to the request.`,
	timeout: 2 * time.Minute,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.BoolVar(&o.probe, "probe", false, "open each port and send one 'V' request at 2400 baud to see if an interface answers")
		o.registerSimulator(fs)
		o.registerVerbose(fs)
	},
	run: runPorts,
}

func runPorts(ctx context.Context, e *env) error {
	if err := e.wantArgs(0); err != nil {
		return err
	}
	var ports []serialport.PortInfo
	if e.opts.simulator {
		ports = []serialport.PortInfo{{Name: "simulator"}}
	} else {
		var err error
		if ports, err = serialport.List(); err != nil {
			return err
		}
	}
	if len(ports) == 0 {
		fmt.Fprintln(e.stdout, "no serial ports found")
		return nil
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	header := "PORT\tUSB ID\tSERIAL\tLIKELY"
	if e.opts.probe {
		header += "\tPROBE"
	}
	fmt.Fprintln(tw, header)
	for _, p := range ports {
		usb, serial, kind := "-", "-", "-"
		if p.IsUSB {
			usb = fmt.Sprintf("%04X:%04X", p.VID, p.PID)
			if p.SerialNumber != "" {
				serial = p.SerialNumber
			}
		}
		if p.Kind != serialport.Unknown {
			kind = p.Kind.String()
		}
		line := p.Name + "\t" + usb + "\t" + serial + "\t" + kind
		if e.opts.probe {
			line += "\t" + e.probe(ctx, p.Name)
		}
		fmt.Fprintln(tw, line)
	}
	return tw.Flush()
}

// probe sends a single 'V' request at 2400 baud and describes the result.
// It does not use NegotiateBaud, whose recovery sends 'R' at 115200 and
// must not reach unknown devices.
func (e *env) probe(ctx context.Context, name string) string {
	t, err := e.open(name)
	if err != nil {
		return "error: " + err.Error()
	}
	c := mk2.NewClient(t, &mk2.Options{Timeout: probeTimeout})
	defer c.Close()
	v, err := c.Version(ctx)
	switch {
	case errors.Is(err, mk2.ErrTimeout):
		return "no answer at 2400 baud"
	case err != nil:
		return "error: " + err.Error()
	}
	return fmt.Sprintf("interface answers, version %d", v.Version)
}

var versionCmd = &command{
	name:    "version",
	summary: "show interface and device firmware versions and the devices found",
	help: `Discovers the devices on the bus by trying addresses from 0 until 3 in a
row do not answer, then reads each device's firmware version. --address is
not used.`,
	timeout: time.Minute,
	device:  true,
	run:     runVersion,
}

func runVersion(ctx context.Context, e *env) error {
	if err := e.wantArgs(0); err != nil {
		return err
	}
	c, baud, err := e.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	v, err := c.Version(ctx)
	if err != nil {
		return fmt.Errorf("interface version: %w", err)
	}
	fmt.Fprintf(e.stdout, "link:      %d baud\n", baud)
	fmt.Fprintf(e.stdout, "interface: version %d (%s)\n", v.Version, versionMode(v.Mode))
	addrs, err := c.Discover(ctx)
	if err != nil {
		return fmt.Errorf("discover devices: %w", err)
	}
	fmt.Fprintf(e.stdout, "devices:   %d found\n", len(addrs))
	for _, a := range addrs {
		fw, err := firmware(ctx, c, a)
		if err != nil {
			fmt.Fprintf(e.stdout, "  address %d: firmware unknown (%v)\n", a, err)
			continue
		}
		fmt.Fprintf(e.stdout, "  address %d: firmware %d\n", a, fw)
	}
	return nil
}

func firmware(ctx context.Context, c *mk2.Client, addr byte) (uint32, error) {
	if err := c.SetAddress(ctx, addr); err != nil {
		return 0, err
	}
	return c.SoftwareVersion(ctx)
}

// versionMode describes the <Mode> byte of a 'V' frame (§3.4).
func versionMode(m byte) string {
	switch m {
	case mk2.VersionModeNoAddress:
		return "VE.Bus, no address selected"
	case mk2.VersionModeRS485:
		return "VE 9-bit RS485 target"
	default:
		return fmt.Sprintf("address %d selected", m)
	}
}

var readSettingCmd = &command{
	name:    "read-setting",
	args:    "<id>",
	summary: "read a setting: raw and scaled value, scale, offset, default, min and max",
	help: `The scaled value is Scale * (raw + Offset) as reported by the device; raw
values are as stored on the device. IDs may be decimal or 0x hex.`,
	timeout: time.Minute,
	device:  true,
	run:     runReadSetting,
}

func runReadSetting(ctx context.Context, e *env) error {
	if err := e.wantArgs(1); err != nil {
		return err
	}
	id, err := e.parseID(e.args[0], 0xFFFF)
	if err != nil {
		return err
	}
	c, err := e.connectDevice(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	s, err := readSetting(ctx, c, uint16(id))
	if err != nil {
		return err
	}
	w := e.stdout
	fmt.Fprintf(w, "setting %d (device %d)\n", id, e.opts.address)
	fmt.Fprintf(w, "  raw      %d\n", s.Raw)
	fmt.Fprintf(w, "  value    %s\n", num(s.Value))
	fmt.Fprintf(w, "  scale    %s (Sc %d)\n", num(s.Info.Scale), s.Info.Sc)
	fmt.Fprintf(w, "  offset   %d\n", s.Info.Offset)
	fmt.Fprintf(w, "  default  %s (raw %d)\n", num(s.Info.DefaultValue), s.Info.Default)
	fmt.Fprintf(w, "  min      %s (raw %d)\n", num(s.Info.MinValue), s.Info.Min)
	fmt.Fprintf(w, "  max      %s (raw %d)\n", num(s.Info.MaxValue), s.Info.Max)
	if s.Info.AccessLevel != nil {
		fmt.Fprintf(w, "  access   %d\n", *s.Info.AccessLevel)
	}
	return nil
}

var readRAMCmd = &command{
	name:    "read-ram",
	args:    "<id>",
	summary: "read a RAM variable: raw and scaled value, scale and offset",
	help: `The scaled value is Scale * (raw + Offset), with raw read as signed when
Sc < 0; for a single-bit variable it is the selected bit. IDs are 0-255,
decimal or 0x hex.`,
	timeout: time.Minute,
	device:  true,
	run:     runReadRAM,
}

func runReadRAM(ctx context.Context, e *env) error {
	if err := e.wantArgs(1); err != nil {
		return err
	}
	id, err := e.parseID(e.args[0], 0xFF)
	if err != nil {
		return err
	}
	c, err := e.connectDevice(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	r, err := readRAM(ctx, c, byte(id))
	if err != nil {
		return err
	}
	w := e.stdout
	fmt.Fprintf(w, "RAM variable %d (device %d)\n", id, e.opts.address)
	fmt.Fprintf(w, "  raw      %d\n", r.Raw)
	fmt.Fprintf(w, "  value    %s\n", num(r.Value))
	if r.Info.Bit != nil {
		fmt.Fprintf(w, "  bit      %d\n", *r.Info.Bit)
	} else {
		fmt.Fprintf(w, "  scale    %s (Sc %d)\n", num(r.Info.Scale), r.Info.Sc)
		fmt.Fprintf(w, "  offset   %d\n", r.Info.Offset)
		fmt.Fprintf(w, "  signed   %t\n", r.Info.Signed)
	}
	return nil
}

var writeSettingCmd = &command{
	name:    "write-setting",
	args:    "<id> <value>",
	summary: "write a setting, check it against the device limits and read it back",
	help: `WARNING: this changes the configuration of a live device. Wrong settings
can damage equipment. The write is refused without --yes.

<value> is in display units (the scaled value shown by read-setting) and is
rounded to the nearest raw step. With --raw it is the raw stored value
(decimal or 0x hex) instead. The value must lie within the minimum and
maximum the device reports. After the write the setting is read back; a
different value is an error.

Pass a negative value after "--": write-setting --yes 63 -- -1.5`,
	timeout: time.Minute,
	device:  true,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.BoolVar(&o.yes, "yes", false, "confirm the write to the device")
		fs.BoolVar(&o.raw, "raw", false, "<value> is the raw stored value, not display units")
	},
	run: runWriteSetting,
}

func runWriteSetting(ctx context.Context, e *env) error {
	if err := e.wantArgs(2); err != nil {
		return err
	}
	id, err := e.parseID(e.args[0], 0xFFFF)
	if err != nil {
		return err
	}
	if !e.opts.yes {
		return e.usageError("refusing to write setting %d without --yes: this changes the configuration of a live device", id)
	}
	var display float64
	var raw uint64
	if e.opts.raw {
		raw, err = strconv.ParseUint(e.args[1], 0, 16)
	} else {
		display, err = strconv.ParseFloat(e.args[1], 64)
	}
	if err != nil {
		return e.usageError("invalid value %q: %v", e.args[1], err)
	}

	c, err := e.connectDevice(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	sid := uint16(id)
	info, err := c.SettingInfo(ctx, sid)
	if err != nil {
		return fmt.Errorf("setting %d: info: %w", id, err)
	}
	value := uint16(raw)
	if !e.opts.raw {
		if value, err = info.Raw(display); err != nil {
			return fmt.Errorf("setting %d: %v is out of range", id, display)
		}
	}
	if value < info.Min || value > info.Max {
		return fmt.Errorf("setting %d: %s (raw %d) is outside the device limits %s to %s (raw %d to %d)",
			id, num(info.Value(value)), value, num(info.Value(info.Min)), num(info.Value(info.Max)), info.Min, info.Max)
	}
	old, err := c.ReadSetting(ctx, sid)
	if err != nil {
		return fmt.Errorf("setting %d: read: %w", id, err)
	}
	if err := c.WriteSetting(ctx, sid, value); err != nil {
		return fmt.Errorf("setting %d: write: %w", id, err)
	}
	got, err := c.ReadSetting(ctx, sid)
	if err != nil {
		return fmt.Errorf("setting %d: read back: %w", id, err)
	}
	if got != value {
		return fmt.Errorf("setting %d: wrote raw %d but read back raw %d", id, value, got)
	}
	fmt.Fprintf(e.stdout, "setting %d (device %d): %s -> %s (raw %d -> %d), read back OK\n",
		id, e.opts.address, num(info.Value(old)), num(info.Value(value)), old, value)
	return nil
}

var dumpCmd = &command{
	name:    "dump",
	summary: "print all readable settings and RAM variables of a device as JSON",
	help: `The protocol cannot list the IDs a device has, so dump tries every ID from 0
to --max-setting and --max-ram (defaults: the ranges documented in the
public MK2 protocol document) and leaves out those the device does not
support. IDs that fail otherwise (no reply, access level) are listed under
"errors".`,
	timeout: 10 * time.Minute,
	device:  true,
	setFlags: func(fs *flag.FlagSet, o *options) {
		fs.UintVar(&o.maxSetting, "max-setting", 65, "highest setting ID to try")
		fs.UintVar(&o.maxRAM, "max-ram", 19, "highest RAM variable ID to try (at most 255)")
	},
	run: runDump,
}

// dumpOutput is the JSON written by dump.
type dumpOutput struct {
	LinkBaud         int            `json:"link_baud"`
	InterfaceVersion uint32         `json:"interface_version"`
	Address          uint           `json:"address"`
	Firmware         uint32         `json:"firmware"`
	Settings         []settingValue `json:"settings"`
	RAM              []ramValue     `json:"ram"`
	Errors           []dumpError    `json:"errors,omitempty"`
}

type settingValue struct {
	ID    uint16      `json:"id"`
	Raw   uint16      `json:"raw"`
	Value float64     `json:"value"`
	Info  settingInfo `json:"info"`
}

type settingInfo struct {
	Sc           int16   `json:"sc"`
	Scale        float64 `json:"scale"`
	Offset       int16   `json:"offset"`
	Default      uint16  `json:"default"`
	DefaultValue float64 `json:"default_value"`
	Min          uint16  `json:"min"`
	MinValue     float64 `json:"min_value"`
	Max          uint16  `json:"max"`
	MaxValue     float64 `json:"max_value"`
	AccessLevel  *byte   `json:"access_level,omitempty"` // Winmon mode 2 only
}

type ramValue struct {
	ID    byte    `json:"id"`
	Raw   uint16  `json:"raw"`
	Value float64 `json:"value"`
	Info  ramInfo `json:"info"`
}

type ramInfo struct {
	Sc     int16   `json:"sc"`
	Scale  float64 `json:"scale"`
	Offset int16   `json:"offset"`
	Signed bool    `json:"signed"`
	Bit    *int    `json:"bit,omitempty"` // single-bit variables only
}

type dumpError struct {
	Kind  string `json:"kind"` // "setting" or "ram"
	ID    uint   `json:"id"`
	Error string `json:"error"`
}

func runDump(ctx context.Context, e *env) error {
	if err := e.wantArgs(0); err != nil {
		return err
	}
	if e.opts.maxSetting > 0xFFFF {
		return e.usageError("--max-setting must be at most 65535")
	}
	if e.opts.maxRAM > 0xFF {
		return e.usageError("--max-ram must be at most 255")
	}
	c, baud, err := e.connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	v, err := c.Version(ctx)
	if err != nil {
		return fmt.Errorf("interface version: %w", err)
	}
	fw, err := firmware(ctx, c, byte(e.opts.address))
	if err != nil {
		return fmt.Errorf("device %d: firmware version: %w", e.opts.address, err)
	}
	out := dumpOutput{
		LinkBaud:         baud,
		InterfaceVersion: v.Version,
		Address:          e.opts.address,
		Firmware:         fw,
		Settings:         []settingValue{},
		RAM:              []ramValue{},
	}
	for id := uint(0); id <= e.opts.maxSetting; id++ {
		s, err := readSetting(ctx, c, uint16(id))
		switch {
		case err == nil:
			out.Settings = append(out.Settings, s)
		case errors.Is(err, mk2.ErrNotSupported):
		case perID(err):
			out.Errors = append(out.Errors, dumpError{"setting", id, err.Error()})
		default:
			return err
		}
	}
	for id := uint(0); id <= e.opts.maxRAM; id++ {
		r, err := readRAM(ctx, c, byte(id))
		switch {
		case err == nil:
			out.RAM = append(out.RAM, r)
		case errors.Is(err, mk2.ErrNotSupported):
		case perID(err):
			out.Errors = append(out.Errors, dumpError{"ram", id, err.Error()})
		default:
			return err
		}
	}
	enc := json.NewEncoder(e.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// perID reports whether a dump error concerns only the ID being read, so
// the dump can go on.
func perID(err error) bool {
	var uc *mk2.UnknownCommandError
	return errors.Is(err, mk2.ErrTimeout) || errors.Is(err, mk2.ErrAccessLevel) || errors.As(err, &uc)
}

// readSetting reads the info and value of a setting.
func readSetting(ctx context.Context, c *mk2.Client, id uint16) (settingValue, error) {
	info, err := c.SettingInfo(ctx, id)
	if err != nil {
		return settingValue{}, fmt.Errorf("setting %d: info: %w", id, err)
	}
	raw, err := c.ReadSetting(ctx, id)
	if err != nil {
		return settingValue{}, fmt.Errorf("setting %d: read: %w", id, err)
	}
	s := settingValue{
		ID:    id,
		Raw:   raw,
		Value: tidy(info.Value(raw)),
		Info: settingInfo{
			Sc:           info.Sc,
			Scale:        tidy(info.Scale()),
			Offset:       info.Offset,
			Default:      info.Default,
			DefaultValue: tidy(info.Value(info.Default)),
			Min:          info.Min,
			MinValue:     tidy(info.Value(info.Min)),
			Max:          info.Max,
			MaxValue:     tidy(info.Value(info.Max)),
		},
	}
	if info.HasAccessLevel {
		s.Info.AccessLevel = &info.AccessLevel
	}
	return s, nil
}

// readRAM reads the info and value of a RAM variable.
func readRAM(ctx context.Context, c *mk2.Client, id byte) (ramValue, error) {
	info, err := c.RAMVarInfo(ctx, uint16(id))
	if err != nil {
		return ramValue{}, fmt.Errorf("RAM variable %d: info: %w", id, err)
	}
	vals, err := c.ReadRAMVar(ctx, id)
	if err != nil {
		return ramValue{}, fmt.Errorf("RAM variable %d: read: %w", id, err)
	}
	r := ramValue{
		ID:    id,
		Raw:   vals[0],
		Value: tidy(info.Value(vals[0])),
		Info: ramInfo{
			Sc:     info.Sc,
			Scale:  tidy(info.Scale()),
			Offset: info.Offset,
			Signed: info.Signed(),
		},
	}
	if info.IsBit() {
		b := info.Bit()
		r.Info.Bit = &b
	}
	return r, nil
}

// parseID parses a setting or RAM ID, decimal or 0x hex, up to max.
func (e *env) parseID(s string, max uint64) (uint64, error) {
	id, err := strconv.ParseUint(s, 0, 16)
	if err != nil || id > max {
		return 0, e.usageError("invalid ID %q: want 0-%d", s, max)
	}
	return id, nil
}

// num formats a scaled value for text output.
func num(v float64) string { return strconv.FormatFloat(v, 'g', 10, 64) }

// tidy rounds v to 10 significant digits, which drops float noise such as
// 9.300000000000001 from 930 * 0.01.
func tidy(v float64) float64 {
	t, _ := strconv.ParseFloat(num(v), 64)
	return t
}
