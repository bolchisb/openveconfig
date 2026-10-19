package simulator

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/mk2"
)

// start runs a Simulator with idle 'V' frames disabled (unless cfg sets
// them) and an mk2.Client on its Port.
func start(t *testing.T, cfg Config, opts *mk2.Options) (*Simulator, *mk2.Client) {
	t.Helper()
	if cfg.VersionInterval == 0 {
		cfg.VersionInterval = -1
	}
	s := New(cfg)
	o := mk2.Options{}
	if opts != nil {
		o = *opts
	}
	if o.Timeout == 0 {
		o.Timeout = 50 * time.Millisecond
	}
	c := mk2.NewClient(s.Port(), &o)
	t.Cleanup(func() { c.Close() })
	return s, c
}

// bus is a three-device bus with a gap at address 2.
func bus() []Device {
	return []Device{
		{Address: 0, Profile: Synthetic12V230()},
		{Address: 1, Profile: Synthetic48V120()},
		{Address: 3, Profile: Synthetic12V230()},
	}
}

// setMode selects the Winmon mode with the optional 'S' bytes (§5),
// leaving the input current limit alone.
func setMode(t *testing.T, c *mk2.Client, mode mk2.WinmonMode) {
	t.Helper()
	var ext byte
	if mode == mk2.WinmonShort {
		ext = mk2.FlagsExt0WinmonShort
	}
	err := c.SetState(context.Background(), mk2.State{
		Switch: mk2.SwitchOn, Flags: mk2.StateFlagLimitDeciAmps, Limit: 0x8000,
		Ext: &mk2.StateExt{FlagsExt0: ext},
	})
	if err != nil {
		t.Fatalf("SetState: %v", err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func check(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func modeName(m mk2.WinmonMode) string {
	if m == mk2.WinmonLong {
		return "mode2"
	}
	return "mode1"
}

func TestClientAgainstSimulator(t *testing.T) {
	for _, mode := range []mk2.WinmonMode{mk2.WinmonShort, mk2.WinmonLong} {
		t.Run(modeName(mode), func(t *testing.T) { runClient(t, mode) })
	}
}

func runClient(t *testing.T, mode mk2.WinmonMode) {
	sim, c := start(t, Config{Devices: bus()}, nil)
	ctx := context.Background()

	// 'V' before an address is set reports 'B' (§3.4).
	v, err := c.Version(ctx)
	check(t, "Version", err)
	if v.Version != DefaultVersion || v.Mode != mk2.VersionModeNoAddress {
		t.Fatalf("Version = %+v", v)
	}
	setMode(t, c, mode)

	// Discovery (§7.2) finds the gap and stops after 3 misses.
	found, err := c.Discover(ctx)
	check(t, "Discover", err)
	if !slices.Equal(found, []byte{0, 1, 3}) {
		t.Fatalf("Discover = %v", found)
	}
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	if v, err := c.Version(ctx); err != nil || v.Mode != 0 {
		t.Fatalf("Version after SetAddress = %+v, %v", v, err)
	}
	if a, err := c.Address(ctx); err != nil || a != 0 {
		t.Fatalf("Address = %d, %v", a, err)
	}

	// Software version of two different devices (§7.3.1).
	if v, err := c.SoftwareVersion(ctx); err != nil || v != Synthetic12V230().Version {
		t.Fatalf("SoftwareVersion(0) = %d, %v", v, err)
	}
	check(t, "SetAddress", c.SetAddress(ctx, 1))
	if v, err := c.SoftwareVersion(ctx); err != nil || v != Synthetic48V120().Version {
		t.Fatalf("SoftwareVersion(1) = %d, %v", v, err)
	}

	// LED status comes from the master (§4).
	if l, err := c.LED(ctx); err != nil || l != Synthetic12V230().LED {
		t.Fatalf("LED = %+v, %v", l, err)
	}

	// 'S' sets the input current limit, clamped to the MasterMultiLED
	// range (§5, §6.2).
	for _, tc := range []struct {
		st   mk2.State
		want uint16
		sw   byte
	}{
		{mk2.State{Switch: mk2.SwitchOn, Flags: mk2.StateFlagLimitDeciAmps, Limit: 123}, 123, 0x30},
		{mk2.State{Switch: mk2.SwitchChargerOnly, Flags: mk2.StateFlagLimitDeciAmps, Limit: 9999}, 500, 0x10},
		{mk2.State{Switch: mk2.SwitchOn, Pot: 128, Scale: 32}, 160, 0x30}, // variant 1: 16 A
	} {
		check(t, "SetState", c.SetState(ctx, tc.st))
		m, err := c.MasterMultiLED(ctx)
		check(t, "MasterMultiLED", err)
		if m.ActualInputLimit != tc.want || m.MinInputLimit != 20 || m.MaxInputLimit != 500 ||
			!m.HasSwitchRegister || m.SwitchRegister != tc.sw {
			t.Fatalf("after %+v: MasterMultiLED = %+v", tc.st, m)
		}
	}

	// Device state (§7.3.2).
	if s, err := c.DeviceState(ctx, mk2.DeviceStateInquire); err != nil || s != (mk2.DeviceState{State: 4}) {
		t.Fatalf("DeviceState(1) = %+v, %v", s, err)
	}
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	if s, err := c.DeviceState(ctx, mk2.DeviceStateForceFloat); err != nil || s != (mk2.DeviceState{State: 9, SubState: 3}) {
		t.Fatalf("DeviceState force float = %+v, %v", s, err)
	}
	var uc *mk2.UnknownCommandError
	if _, err := c.DeviceState(ctx, 7); !errors.As(err, &uc) || uc.Reason != 1 {
		t.Fatalf("DeviceState(7): %v", err)
	}

	// Info frames (§6.1), scaled with the RAM info (§7.3.9).
	dc, err := c.DCInfo(ctx)
	check(t, "DCInfo", err)
	if dc != (mk2.DCInfo{Voltage: 1352, CurrentCharging: 2150, InverterPeriod: 136}) {
		t.Fatalf("DCInfo = %+v", dc)
	}
	ubat, err := c.RAMVarInfo(ctx, 4)
	check(t, "RAMVarInfo(4)", err)
	if !near(ubat.Value(dc.Voltage), 13.52) {
		t.Errorf("DC voltage = %v V", ubat.Value(dc.Voltage))
	}
	period, err := c.RAMVarInfo(ctx, 7)
	check(t, "RAMVarInfo(7)", err)
	if f := 10 / period.Value(uint16(dc.InverterPeriod)); math.Abs(f-50) > 0.1 {
		t.Errorf("inverter frequency = %v Hz", f)
	}
	for _, tc := range []struct {
		phase int
		want  mk2.ACInfo
	}{
		{1, mk2.ACInfo{BFFactor: 1, InverterFactor: 1, State: 9, PhaseInfo: 0x0A, MainsVoltage: 23050,
			MainsCurrent: 412, InverterVoltage: 23050, InverterCurrent: 95, MainsPeriod: 195}},
		{2, mk2.ACInfo{BFFactor: 1, InverterFactor: 1, State: 4, PhaseInfo: 0x07, InverterVoltage: 12010,
			InverterCurrent: 1830}},
		{3, mk2.ACInfo{BFFactor: 1, InverterFactor: 1, State: 9, PhaseInfo: 0x06, MainsVoltage: 23050,
			MainsCurrent: 412, InverterVoltage: 23050, InverterCurrent: 95, MainsPeriod: 195}},
	} {
		if ac, err := c.ACInfo(ctx, tc.phase); err != nil || ac != tc.want {
			t.Fatalf("ACInfo(%d) = %+v, %v", tc.phase, ac, err)
		}
	}
	if _, err := c.ACInfo(ctx, 4); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("ACInfo(4) on a 3-phase bus: %v", err)
	}

	// Settings: read, write, read back (§7.3.4, §7.3.6-7); devices are
	// independent.
	if v, err := c.ReadSetting(ctx, 2); err != nil || v != 1440 {
		t.Fatalf("ReadSetting(2) = %d, %v", v, err)
	}
	check(t, "WriteSetting", c.WriteSetting(ctx, 2, 1450))
	if v, err := c.ReadSetting(ctx, 2); err != nil || v != 1450 {
		t.Fatalf("ReadSetting(2) after write = %d, %v", v, err)
	}
	if _, err := c.ReadSetting(ctx, 999); !errors.Is(err, mk2.ErrNotSupported) {
		t.Fatalf("ReadSetting(999): %v", err)
	}
	check(t, "SetAddress", c.SetAddress(ctx, 3))
	if v, err := c.ReadSetting(ctx, 2); err != nil || v != 1440 {
		t.Fatalf("ReadSetting(2) on device 3 = %d, %v", v, err)
	}
	check(t, "SetAddress", c.SetAddress(ctx, 0))

	// RAM variables (§7.3.3, §7.3.5).
	check(t, "WriteRAMVar", c.WriteRAMVar(ctx, 4, 1300))
	if v, err := c.ReadRAMVar(ctx, 4, 5, 13); err != nil || !slices.Equal(v, []uint16{1300, 2150, 16200}) {
		t.Fatalf("ReadRAMVar = %v, %v", v, err)
	}
	if _, err := c.ReadRAMVar(ctx, 99); !errors.Is(err, mk2.ErrNotSupported) {
		t.Fatalf("ReadRAMVar(99): %v", err)
	}

	// Setting info and scaling (§7.3.8).
	si, err := c.SettingInfo(ctx, 2)
	check(t, "SettingInfo(2)", err)
	want := mk2.SettingInfo{Sc: -100, Default: 1440, Min: 800, Max: 1750}
	if mode == mk2.WinmonLong {
		want.HasAccessLevel = true
	}
	if si != want || !near(si.Value(si.Default), 14.4) {
		t.Fatalf("SettingInfo(2) = %+v", si)
	}
	si, err = c.SettingInfo(ctx, 63)
	check(t, "SettingInfo(63)", err)
	if !near(si.Value(si.Default), 0.2) || !near(si.Value(si.Min), -2) {
		t.Fatalf("SettingInfo(63) = %+v: default %v, min %v", si, si.Value(si.Default), si.Value(si.Min))
	}
	si, err = c.SettingInfo(ctx, 8)
	check(t, "SettingInfo(8)", err)
	if !near(si.Value(si.Default), 168*60) {
		t.Fatalf("SettingInfo(8) default = %v", si.Value(si.Default))
	}
	if si, err := c.SettingInfo(ctx, 13); err != nil || (mode == mk2.WinmonLong && si.AccessLevel != 2) {
		t.Fatalf("SettingInfo(13) = %+v, %v", si, err)
	}
	if _, err := c.SettingInfo(ctx, 999); !errors.Is(err, mk2.ErrNotSupported) {
		t.Fatalf("SettingInfo(999): %v", err)
	}

	// RAM info and scaling (§7.3.9), on the inverting device.
	check(t, "SetAddress", c.SetAddress(ctx, 1))
	ibat, err := c.RAMVarInfo(ctx, 5)
	check(t, "RAMVarInfo(5)", err)
	raw, err := c.ReadRAMVar(ctx, 5)
	check(t, "ReadRAMVar(5)", err)
	if !ibat.Signed() || !near(ibat.Value(raw[0]), -44.2) {
		t.Fatalf("IBat = %v (%+v, raw %#x)", ibat.Value(raw[0]), ibat, raw[0])
	}
	vs, err := c.RAMVarInfo(ctx, 10)
	check(t, "RAMVarInfo(10)", err)
	raw, err = c.ReadRAMVar(ctx, 10)
	check(t, "ReadRAMVar(10)", err)
	if !vs.IsBit() || vs.Bit() != 3 || vs.Value(raw[0]) != 1 {
		t.Fatalf("virtual switch = %+v, raw %#x", vs, raw[0])
	}
	if _, err := c.RAMVarInfo(ctx, 99); !errors.Is(err, mk2.ErrNotSupported) {
		t.Fatalf("RAMVarInfo(99): %v", err)
	}

	// Snapshot (§6 'F' 6, §7.3.12): every device copies the values, which
	// can be read once.
	check(t, "TakeSnapshot", c.TakeSnapshot(ctx, 4, 5))
	sim.SetRAMVar(1, 4, 4000)
	if v, err := c.ReadSnapshot(ctx); err != nil || !slices.Equal(v, []uint16{5168, uint16(0x10000 - 4420)}) {
		t.Fatalf("ReadSnapshot(1) = %v, %v", v, err)
	}
	if v, err := c.ReadSnapshot(ctx); err != nil || len(v) != 0 {
		t.Fatalf("second ReadSnapshot = %v, %v", v, err)
	}
	check(t, "SetAddress", c.SetAddress(ctx, 0))
	if v, err := c.ReadSnapshot(ctx); err != nil || !slices.Equal(v, []uint16{1300, 2150}) {
		t.Fatalf("ReadSnapshot(0) = %v, %v", v, err)
	}

	// WriteViaID (§7.3.11) is mode 2 only.
	err = c.WriteViaID(ctx, mk2.ViaIDSetting, 3, 1390)
	if mode == mk2.WinmonShort {
		if !errors.As(err, &uc) {
			t.Fatalf("WriteViaID in mode 1: %v", err)
		}
	} else {
		check(t, "WriteViaID setting", err)
		if v, err := c.ReadSetting(ctx, 3); err != nil || v != 1390 {
			t.Fatalf("ReadSetting(3) after WriteViaID = %d, %v", v, err)
		}
		check(t, "WriteViaID RAM", c.WriteViaID(ctx, 0, 4, 1234))
		if v, err := c.ReadRAMVar(ctx, 4); err != nil || v[0] != 1234 {
			t.Fatalf("ReadRAMVar(4) after WriteViaID = %v, %v", v, err)
		}
		if err := c.WriteViaID(ctx, mk2.ViaIDSetting, 13, 1); !errors.Is(err, mk2.ErrAccessLevel) {
			t.Fatalf("WriteViaID on access level 2 setting: %v", err)
		}
		if err := c.WriteViaID(ctx, mk2.ViaIDSetting, 200, 1); !errors.As(err, &uc) || uc.Reason != 2 {
			t.Fatalf("WriteViaID on unknown setting: %v", err)
		}
	}

	// Unknown Winmon command (§7.3).
	if _, err := c.Winmon(ctx, 0x7F, 1, 2); !errors.As(err, &uc) || uc.Reason != 0 {
		t.Fatalf("Winmon(0x7F): %v", err)
	}
}

func TestTunnels(t *testing.T) {
	ctx := context.Background()
	_, c := start(t, Config{Devices: bus()}, &mk2.Options{Tunnel: mk2.TunnelXYZ})
	check(t, "SetAddress", c.SetAddress(ctx, 1))
	for range 4 { // X, Y, Z, X
		if v, err := c.ReadSetting(ctx, 2); err != nil || v != 5760 {
			t.Fatalf("XYZ ReadSetting = %d, %v", v, err)
		}
	}

	// 'x/y/z' carry the address; none is set with 'A'.
	_, c = start(t, Config{Devices: bus()}, &mk2.Options{Tunnel: mk2.TunnelXYZAddressed})
	c.SetDestination(1)
	if v, err := c.ReadSetting(ctx, 2); err != nil || v != 5760 {
		t.Fatalf("xyz ReadSetting(dest 1) = %d, %v", v, err)
	}
	c.SetDestination(2)
	if _, err := c.ReadSetting(ctx, 2); !errors.Is(err, mk2.ErrTimeout) {
		t.Fatalf("xyz ReadSetting(dest 2): %v", err)
	}
}

func TestEmptyBus(t *testing.T) {
	ctx := context.Background()
	_, c := start(t, Config{Devices: []Device{}}, &mk2.Options{Timeout: 20 * time.Millisecond})
	if l, err := c.LED(ctx); err != nil || l != (mk2.LEDStatus{On: 0x1F, Blink: 0x1F}) {
		t.Fatalf("LED = %+v, %v", l, err)
	}
	if found, err := c.Discover(ctx); err != nil || len(found) != 0 {
		t.Fatalf("Discover = %v, %v", found, err)
	}
}
