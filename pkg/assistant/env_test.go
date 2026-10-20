package assistant

import (
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

func TestLabels(t *testing.T) {
	d := Device{
		MultiVersion: 2627560, PNominal: 8300, DCCode: 24, ACVolts: 228.6, WinmonIDOffset: 4,
		VersionExtra:   []byte{0x07, 0x01, 0x10},
		PackageVersion: 116, DownloadedBy: 9004248,
		Settings: []DeviceSetting{
			{ID: 64, Name: "sCap", Scale: 1, Default: 100, Min: 10, Max: 9999, Value: 200},
			{ID: 70, Name: "sNoScale", Value: 3},
			{ID: 130, Name: "GridThing", Scale: 1, Value: 5},
			{ID: 128, Name: "GridSettingsInt0", Scale: 1, Max: 9, Value: 1},
			{ID: 71, Name: "a@b", Scale: 1, Value: 1},
		},
		Flags: []DeviceFlag{{Name: "fOn", Default: true}, {Name: "fOff"}, {Name: "x@y"}},
		Extra: map[string]asm.Value{"cDeviceInfo": asm.IntVal(1), "hwAC": asm.IntVal(120)},
	}
	l := Labels(d)
	want := map[string]int64{
		"cMultiVersion": 2627560, "cPNominal": 8300, "hwDC": 48, "hwAC": 120,
		"cIInvNominal": 3630, "cWinmonIDOffset": 4, "cVEConfigVersion": 9004248,
		"cVLLVersion": 0x007, "cVLLSystem": 0x1000, "packageversion": 116, "DownloadedBy": 9004248,
		"cDeviceInfo": 1,
		"sCap_VEC":    200, "sCap_scale": 1, "sCap_default": 100, "sCap_minimum": 10, "sCap_maximum": 9999, "sCap_offset": 0,
		"sNoScale_VEC": 3, "GridThing_VEC": 5, "GridSettingsInt0_VEC": 1, "GridSettingsInt0_maximum": 9,
		"eefOn_default": 1, "eefOff_default": 0,
	}
	for k, v := range want {
		if got, ok := l[k]; !ok || got.I != v {
			t.Errorf("%s = %v %v, want %d", k, got, ok, v)
		}
	}
	for _, k := range []string{"sNoScale_scale", "GridThing_scale", "a@b_VEC", "eex@y_default", "cMultiFamily", "cFirmwareSupportsVEBusBMS"} {
		if _, ok := l[k]; ok {
			t.Errorf("%s defined", k)
		}
	}
	if v := l["ConnectionBoardWithPIC"]; v.K != asm.Bool || v.I != 1 {
		t.Errorf("ConnectionBoardWithPIC = %v", v)
	}
	if g := l["GridCodesRequiringENS"]; len(g.L) != 2 || g.L[0].I != 3 || g.L[1].I != 4 {
		t.Errorf("GridCodesRequiringENS = %v", g)
	}

	// Without version bytes those labels stay out; 0xFF reads as 0;
	// a given IInvNominal is kept; hwAC rounds to the nearest.
	l = Labels(Device{PNominal: 3000, DCCode: 12, ACVolts: 250, WinmonIDOffset: 0xFF, IInvNominal: 1234})
	if _, ok := l["cVLLVersion"]; ok {
		t.Error("cVLLVersion without version bytes")
	}
	if l["cWinmonIDOffset"].I != 0 || l["cIInvNominal"].I != 1234 || l["hwAC"].I != 230 || l["hwDC"].I != 24 {
		t.Errorf("labels %v", l)
	}
	if nearestAC(110) != 120 || nearestAC(270) != 276 {
		t.Error("nearestAC")
	}
}
