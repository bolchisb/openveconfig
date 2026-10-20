package mk2

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestSoftwareVersionMode1(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		switch req.Data[0] {
		case WCmdSendSoftwareVersionPart0: // old MK2 adds a trailing 0x00 (Appendix 1)
			d.send(withCS(t, "06 FF 'W 82 93 3E 00"))
		case WCmdSendSoftwareVersionPart1:
			d.send(withCS(t, "06 FF 'W 83 11 00 00"))
		}
	})
	v, err := c.SoftwareVersion(context.Background())
	if err != nil || v != 0x00113E93 {
		t.Fatalf("SoftwareVersion = %#x, %v", v, err)
	}
	// Appendix 1 requests, checksums included.
	reqs := d.requests()
	for i, want := range []string{"05 FF 'W 05 00 00 A0", "05 FF 'W 06 00 00 9F"} {
		if !bytes.Equal(reqs[i], unhex(t, want)) {
			t.Errorf("request %d = % X, want %s", i, reqs[i], want)
		}
	}
}

func TestDeviceState(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		d.sendFrame(wReply(WReplyDeviceState, 9, 2))
	})
	s, err := c.DeviceState(context.Background(), DeviceStateForceFloat)
	if err != nil || s != (DeviceState{State: 9, SubState: 2}) {
		t.Fatalf("DeviceState = %+v, %v", s, err)
	}
	if want := withCS(t, "05 FF 'W 0E 03 00"); !bytes.Equal(d.requests()[0], want) {
		t.Fatalf("request = % X", d.requests()[0])
	}
}

func TestReadRAMVar(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		ids := req.Data[1:]
		if ids[0] == 99 {
			d.sendFrame(wReply(WReplyRAMNotSupported, 0, 0))
			return
		}
		p := []byte{WReplyReadRAMOK}
		for _, id := range ids {
			p = append(p, id, 0x10)
		}
		d.sendFrame(wReply(p...))
	})
	ctx := context.Background()
	// Appendix 1 "Request the State of Charge value": 05 FF 'W 30 0D 00 68;
	// the reply carries an extra value [DD DD] that is ignored.
	v, err := c.ReadRAMVar(ctx, 13)
	if err != nil || len(v) != 1 || v[0] != 0x100D {
		t.Fatalf("ReadRAMVar(13) = %v, %v", v, err)
	}
	if !bytes.Equal(d.requests()[0], unhex(t, "05 FF 'W 30 0D 00 68")) {
		t.Fatalf("request = % X", d.requests()[0])
	}
	v, err = c.ReadRAMVar(ctx, 4, 5, 14, 15, 16, 17)
	if err != nil || len(v) != 6 || v[5] != 0x1011 {
		t.Fatalf("ReadRAMVar(6 IDs) = %v, %v", v, err)
	}
	if _, err := c.ReadRAMVar(ctx, 99); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unsupported: %v", err)
	}
	if _, err := c.ReadRAMVar(ctx, 1, 2, 3, 4, 5, 6, 7); err == nil {
		t.Fatal("7 IDs accepted")
	}
}

func TestReadSetting(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		if req.Data[1] == 200 {
			d.sendFrame(wReply(WReplySettingNotSupported, 0, 0))
			return
		}
		d.sendFrame(wReply(WReplyReadSettingOK, 0x40, 0x0B, 0xAA)) // extra byte ignored
	})
	ctx := context.Background()
	v, err := c.ReadSetting(ctx, 0x0102)
	if err != nil || v != 0x0B40 {
		t.Fatalf("ReadSetting = %#x, %v", v, err)
	}
	if want := withCS(t, "05 FF 'W 31 02 01"); !bytes.Equal(d.requests()[0], want) {
		t.Fatalf("request = % X", d.requests()[0])
	}
	if _, err := c.ReadSetting(ctx, 200); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unsupported: %v", err)
	}
}

func TestWriteData(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		switch req.Data[0] {
		case WCmdWriteSetting, WCmdWriteRAMVar:
			// no reply (§7.3.5, §7.3.6)
		case WCmdWriteData:
			reqs := d.requests()
			prev, _ := Decode(reqs[len(reqs)-2])
			if prev.Data[0] == WCmdWriteSetting {
				d.sendFrame(wReply(WReplyWriteSettingOK, 0, 0))
			} else {
				d.sendFrame(wReply(WReplyWriteRAMOK, 0, 0))
			}
		}
	})
	ctx := context.Background()
	if err := c.WriteSetting(ctx, 2, 0x0B40); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteRAMVar(ctx, 0x0105, 0xFFFF); err != nil {
		t.Fatal(err)
	}
	reqs := d.requests()
	for i, want := range []string{
		"05 FF 'W 33 02 00", "05 FF 'W 34 40 0B",
		"05 FF 'W 32 05 01", "05 FF 'W 34 FF FF",
	} {
		if !bytes.Equal(reqs[i], withCS(t, want)) {
			t.Errorf("request %d = % X, want %s", i, reqs[i], want)
		}
	}
}

func TestWriteViaID(t *testing.T) {
	c, d := startDevice(t, &Options{WriteMethod: WriteMethodViaID}, func(d *device, req Frame) {
		flags, id := req.Data[1], req.Data[2]
		switch {
		case id == 0x80:
			d.sendFrame(wReply(WReplyAccessLevelRequired, 0, 0))
		case id == 0x81:
			d.sendFrame(wReply(WReplyUnknownCommand, 0x01, 0))
		case flags&ViaIDSetting != 0:
			d.sendFrame(wReply(WReplyWriteSettingOK, 0, 0))
		default:
			d.sendFrame(wReply(WReplyWriteRAMOK, 0, 0))
		}
	})
	ctx := context.Background()
	if err := c.WriteSetting(ctx, 64, 200); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteRAMVar(ctx, 10, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteViaID(ctx, ViaIDSetting|ViaIDRAMOnly, 3, 0x1234); err != nil {
		t.Fatal(err)
	}
	if err := c.WriteSetting(ctx, 0x80, 1); !errors.Is(err, ErrAccessLevel) {
		t.Fatalf("access level: %v", err)
	}
	var uc *UnknownCommandError
	if err := c.WriteSetting(ctx, 0x81, 1); !errors.As(err, &uc) || uc.Reason != 1 {
		t.Fatalf("unknown command: %v", err)
	}
	if err := c.WriteSetting(ctx, 0x100, 1); err == nil {
		t.Fatal("16-bit ID accepted")
	}
	reqs := d.requests()
	for i, want := range []string{
		"07 FF 'W 37 01 40 C8 00",
		"07 FF 'W 37 00 0A 01 00",
		"07 FF 'W 37 03 03 34 12",
	} {
		if !bytes.Equal(reqs[i], withCS(t, want)) {
			t.Errorf("request %d = % X, want %s", i, reqs[i], want)
		}
	}
}

func TestIgnoreMissingWriteAck(t *testing.T) {
	silent := func(d *device, req Frame) {}
	c, _ := startDevice(t, &Options{Timeout: 20 * time.Millisecond}, silent)
	if err := c.WriteSetting(context.Background(), 130, 1); !errors.Is(err, ErrTimeout) {
		t.Fatalf("default: %v", err)
	}
	c, _ = startDevice(t, &Options{Timeout: 20 * time.Millisecond, IgnoreMissingWriteAck: true}, silent)
	if err := c.WriteSetting(context.Background(), 130, 1); err != nil {
		t.Fatalf("IgnoreMissingWriteAck: %v", err)
	}
}

func TestSettingInfoMode1(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		if req.Data[1] == 99 {
			d.sendFrame(wReply(WReplySettingInfoScale, 0, 0)) // Sc 0: nothing else follows
			return
		}
		// Five W frames, at most three per MK2 frame (§7.3).
		d.sendFrame(wReply(0x89, 0x9C, 0xFF, 0x8A, 0x00, 0x00, 0x8B, 0x40, 0x0B))
		d.sendFrame(wReply(0x8C, 0x60, 0x09, 0x8D, 0x20, 0x0D))
	})
	ctx := context.Background()
	s, err := c.SettingInfo(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := SettingInfo{Sc: -100, Offset: 0, Default: 2880, Min: 2400, Max: 3360}
	if s != want {
		t.Fatalf("SettingInfo = %+v, want %+v", s, want)
	}
	if !near(s.Value(s.Default), 28.8, 1e-9) {
		t.Errorf("default = %v V", s.Value(s.Default))
	}
	if want := withCS(t, "05 FF 'W 35 02 00"); !bytes.Equal(d.requests()[0], want) {
		t.Fatalf("request = % X", d.requests()[0])
	}
	if _, err := c.SettingInfo(ctx, 99); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unsupported: %v", err)
	}
}

func TestSettingInfoMode2(t *testing.T) {
	c, _ := startDevice(t, &Options{WinmonMode: WinmonLong}, func(d *device, req Frame) {
		if req.Data[1] == 99 {
			d.sendFrame(wReply(WReplyReadSettingOK)) // "0x86 without data"
			return
		}
		d.sendFrame(wReply(0x89, 0x9C, 0xFF, 0x00, 0x00, 0x40, 0x0B, 0x60, 0x09, 0x20, 0x0D, 0x02))
	})
	ctx := context.Background()
	s, err := c.SettingInfo(ctx, 2)
	want := SettingInfo{Sc: -100, Default: 2880, Min: 2400, Max: 3360, AccessLevel: 2, HasAccessLevel: true}
	if err != nil || s != want {
		t.Fatalf("SettingInfo = %+v, %v", s, err)
	}
	if _, err := c.SettingInfo(ctx, 99); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unsupported: %v", err)
	}
}

func TestSettingInfoCommandOption(t *testing.T) {
	c, d := startDevice(t, &Options{SettingInfoCommand: 0x3C}, func(d *device, req Frame) {
		d.sendFrame(wReply(0x89, 1, 0, 0x8A, 0, 0, 0x8B, 0, 0))
		d.sendFrame(wReply(0x8C, 0, 0, 0x8D, 1, 0))
	})
	if _, err := c.SettingInfo(context.Background(), 5); err != nil {
		t.Fatal(err)
	}
	if want := withCS(t, "05 FF 'W 3C 05 00"); !bytes.Equal(d.requests()[0], want) {
		t.Fatalf("request = % X", d.requests()[0])
	}
}

func TestRAMVarInfo(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		if req.Data[1] == 99 {
			d.sendFrame(wReply(WReplyRAMInfoScale, 0, 0))
			return
		}
		d.sendFrame(wReply(0x8E, 0x57, 0x78, 0x8F, 0x00, 0x01))
	})
	ctx := context.Background()
	r, err := c.RAMVarInfo(ctx, 4)
	if err != nil || r != (RAMInfo{Sc: 30807, Offset: 256}) {
		t.Fatalf("RAMVarInfo = %+v, %v", r, err)
	}
	// Appendix 1 "Request the scale and offset information of the DC voltage".
	if !bytes.Equal(d.requests()[0], unhex(t, "05 FF 'W 36 04 00 6B")) {
		t.Fatalf("request = % X", d.requests()[0])
	}
	if _, err := c.RAMVarInfo(ctx, 99); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("unsupported: %v", err)
	}

	c, _ = startDevice(t, &Options{WinmonMode: WinmonLong}, func(d *device, req Frame) {
		d.sendFrame(wReply(0x8E, 0x64, 0x80, 0x00, 0x00))
	})
	if r, err := c.RAMVarInfo(ctx, 5); err != nil || r != (RAMInfo{Sc: -32668}) {
		t.Fatalf("mode 2 RAMVarInfo = %+v, %v", r, err)
	}
}

func TestReadSnapshot(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		d.sendFrame(wReply(WReplySnapshot, 0x10, 0x00, 0xF0, 0xFF, 0x00))
	})
	v, err := c.ReadSnapshot(context.Background())
	if err != nil || len(v) != 2 || v[0] != 0x10 || v[1] != 0xFFF0 {
		t.Fatalf("ReadSnapshot = %v, %v", v, err)
	}
	if want := withCS(t, "05 FF 'W 38 00 00"); !bytes.Equal(d.requests()[0], want) {
		t.Fatalf("request = % X", d.requests()[0])
	}
}

func TestWinmonUnknownCommand(t *testing.T) {
	c, _ := startDevice(t, nil, func(d *device, req Frame) {
		d.sendFrame(wReply(WReplyUnknownCommand, 0x00, 0x00))
	})
	_, err := c.Winmon(context.Background(), 0x7F, 1, 2)
	var uc *UnknownCommandError
	if !errors.As(err, &uc) || uc.Reason != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitWFrames(t *testing.T) {
	fs := SplitWFrames([]byte{0x8E, 1, 2, 0x8F, 3, 4, 0x00})
	if len(fs) != 2 || fs[1] != (WFrame{0x8F, 3, 4}) {
		t.Fatalf("SplitWFrames = %v", fs)
	}
}

// TestSendAccessPassword checks the 0x3A frame and the replies: 0x9C is
// accepted, any other reply (or "unknown command") is refused. The
// passwords are made-up test values.
func TestSendAccessPassword(t *testing.T) {
	c, d := startDevice(t, nil, func(d *device, req Frame) {
		switch string(req.Data[1:]) {
		case "TEST1":
			d.sendFrame(wReply(WReplyAccessPasswordOK, 0, 0))
		case "OLD":
			d.sendFrame(wReply(WReplyUnknownCommand, 0, 0))
		default:
			d.sendFrame(wReply(WReplyAccessLevelRequired, 0, 0))
		}
	})
	ctx := context.Background()
	if err := c.SendAccessPassword(ctx, "TEST1"); err != nil {
		t.Fatalf("accepted password: %v", err)
	}
	if err := c.SendAccessPassword(ctx, "WRONG"); !errors.Is(err, ErrPasswordRefused) {
		t.Errorf("wrong password: %v", err)
	}
	if err := c.SendAccessPassword(ctx, "OLD"); !errors.Is(err, ErrPasswordRefused) {
		t.Errorf("unknown command: %v", err)
	}
	for _, bad := range []string{"", "pass word\x01", "parolă"} {
		if err := c.SendAccessPassword(ctx, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got, want := d.requests()[0], withCS(t, "08 FF 'W 3A 'T 'E 'S 'T '1"); !bytes.Equal(got, want) {
		t.Errorf("request % X, want % X", got, want)
	}
}

// TestWriteDataAccessLevel checks that "access level required" in reply to
// CommandWriteData is reported as ErrAccessLevel.
func TestWriteDataAccessLevel(t *testing.T) {
	c, _ := startDevice(t, &Options{WriteMethod: WriteMethodData}, func(d *device, req Frame) {
		if req.Data[0] == WCmdWriteData {
			d.sendFrame(wReply(WReplyAccessLevelRequired, 0, 0))
		}
	})
	if err := c.WriteSetting(context.Background(), 13, 1); !errors.Is(err, ErrAccessLevel) {
		t.Fatalf("WriteSetting: %v", err)
	}
}
