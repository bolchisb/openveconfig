package firmware

import (
	"errors"
	"testing"

	"github.com/bolchisb/openveconfig/internal/vfftest"
	"github.com/bolchisb/openveconfig/pkg/vff"
)

// mainFile builds a one-part Main firmware file with the given metadata.
func mainFile(t *testing.T, meta string) *vff.File {
	t.Helper()
	data := vfftest.Part3(vfftest.Spec3{Format: 7, Meta: meta, Desc: []byte("C"),
		CL: vfftest.Records(1, 64, 1), EL: nil})
	f, err := vff.Parse(data)
	if err != nil {
		t.Fatalf("meta %q: %v", meta, err)
	}
	return f
}

func TestPreflight(t *testing.T) {
	for _, tc := range []struct {
		name   string
		meta   string
		target Target
		want   error
	}{
		{"match same version", "Part=Main,Version=2003456,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: -1}, nil},
		{"upgrade within family", "Part=Main,Version=2003900,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: -1}, nil},
		{"family mismatch", "Part=Main,Version=2101000,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: -1}, ErrFirmwareMismatch},
		{"downgrade", "Part=Main,Version=2003100,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: -1}, ErrDowngrade},
		{"no version", "Part=Main,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: -1}, ErrUnsupportedTarget},
		{"bootblock must-be mismatch", "Part=Main,Version=2003456,MustBeBFV=5,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: 4}, ErrBootblockMismatch},
		{"bootblock too old", "Part=Main,Version=2003456,MinBFV=10,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: 4}, ErrBootblockUpdateRequired},
		{"bootblock ok", "Part=Main,Version=2003456,MinBFV=10,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: 12}, nil},
		{"bootblock unknown skips check", "Part=Main,Version=2003456,MustBeBFV=5,CL=1,EL=0", Target{FirmwareNumber: 2003456, BootblockVersion: -1}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Preflight(mainFile(t, tc.meta), tc.target)
			if !errors.Is(err, tc.want) {
				t.Errorf("Preflight = %v, want %v", err, tc.want)
			}
		})
	}
}
