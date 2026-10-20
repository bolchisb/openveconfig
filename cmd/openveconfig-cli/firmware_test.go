package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bolchisb/openveconfig/internal/vfftest"
)

func TestFirmwareInfo(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.vff")
	os.WriteFile(good, vfftest.File7(), 0o644)
	code, out, errOut := cli(t, "firmware", "info", good)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "version   2623457", "format    7", "parts     3 in 2 group(s)", "all checks passed",
		"part 2: Bootblock", "records: 0 ML, 2 EEPROM, 3 code; descriptor 33 bytes", "Multi Test 12V", "Foo", "bar")

	bad := vfftest.File7()
	bad[len(bad)-5]++
	corrupt := filepath.Join(dir, "bad.vff")
	os.WriteFile(corrupt, bad, 0o644)
	code, _, errOut = cli(t, "firmware", "info", corrupt)
	if code != exitError {
		t.Errorf("corrupt file: exit %d", code)
	}
	wantContains(t, errOut, "part 3")

	for _, a := range [][]string{{"firmware"}, {"firmware", "flash", good}, {"firmware", "info"}} {
		if code, _, _ := cli(t, a...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", a, code, exitUsage)
		}
	}
}
