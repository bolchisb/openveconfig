package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/internal/rvsctest"
	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/transfer"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
	"github.com/bolchisb/openveconfig/pkg/vebus/system/bus"
)

func TestAssistantsProbe(t *testing.T) {
	// The simulated devices do not know 0x5F.
	code, out, errOut := cli(t, args("assistants", "probe")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "Winmon 0x5F to device 0", "arg     0: unknown command (reason 0x00)")

	// A made-up device that answers arguments below 2.
	withSimulator(t, func(s *simulator.Simulator) {
		s.Handle('W', func(l *simulator.Line, req mk2.Frame) {
			if req.Data[0] != mk2.WCmdVLLBlockRead {
				return
			}
			if req.Data[1] >= 2 {
				l.Send(mk2.Frame{Command: 'W', Data: []byte{mk2.WReplyUnknownCommand, 1, 0}})
				return
			}
			l.Send(mk2.Frame{Command: 'W', Data: []byte{0xA0, req.Data[1], 0x11, 0x22}})
		})
	})
	code, out, errOut = cli(t, args("assistants", "probe", "--count", "3", "--address", "1")...)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	wantContains(t, out, "Winmon 0x5F to device 1",
		"arg     0: reply 0xA0, 4 bytes: A0 00 11 22",
		"arg     1: reply 0xA0, 4 bytes: A0 01 11 22",
		"arg     2: unknown command (reason 0x01)")
}

func TestAssistantsProbeUsage(t *testing.T) {
	for _, a := range [][]string{
		args("assistants"),
		args("assistants", "write"),
		args("assistants", "copy"),
		args("assistants", "clear", "extra"),
		args("assistants", "read", "--yes"),
		args("assistants", "write", "a.rvsc", "-o", "b.rvsc"),
		args("assistants", "read", "a.rvsc"),
		args("assistants", "read", "a.rvsc", "-o", "a.rvsc"),
		args("assistants", "probe", "--count", "0"),
		args("assistants", "probe", "--count", "257"),
		args("assistants", "probe", "--arg", "65536"),
		args("assistants", "probe", "--arg", "65535", "--count", "2"),
	} {
		if code, _, _ := cli(t, a...); code != exitUsage {
			t.Errorf("%v: exit %d, want %d", a, code, exitUsage)
		}
	}
}

// assistantBus makes --simulator use a fresh bus with the demo unit
// (demoAssistantUnit) holding the area img and the system record rec (the
// factory record when zero), with short waits. It returns a pointer to the
// simulator of the last run.
func assistantBus(t *testing.T, img []byte, rec [mk2.SystemRecordLen]byte) **simulator.Simulator {
	t.Helper()
	orig, origClient, origSession, origTransfer := newSimulator, clientOptions, sessionOptions, transferOptions
	t.Cleanup(func() {
		newSimulator, clientOptions, sessionOptions, transferOptions = orig, origClient, origSession, origTransfer
	})
	clientOptions.RawTimeout = 100 * time.Millisecond
	sessionOptions = bus.Options{ResetWait: 100 * time.Millisecond, PostResetWindow: 5 * time.Second}
	transferOptions = transfer.Options{ResetWait: 20 * time.Millisecond, BackWindow: 2 * time.Second}
	var last *simulator.Simulator
	newSimulator = func() *simulator.Simulator {
		d := demoAssistantUnit()
		d.Assistants.Image = img
		d.System.Record = rec
		last = simulator.New(simulator.Config{Devices: []simulator.Device{d}, VersionInterval: -1, ResetDuration: 50 * time.Millisecond})
		return last
	}
	return &last
}

// factory is the factory record: configured for assistants.
var factory [mk2.SystemRecordLen]byte

// area returns the assistant area of the simulated unit and the block
// writes it received.
func area(t *testing.T, s *simulator.Simulator) ([]byte, int) {
	t.Helper()
	st, ok := s.Assistants(0)
	if !ok {
		t.Fatal("no assistant area")
	}
	return st.Image, st.Writes
}

// backupSpec is a one-unit backup of the demo unit (long ID 0x10000001)
// whose assistant area is img in compact form.
func backupSpec(img []byte) rvsctest.File {
	f := rvscSpec()
	f.Units[0].LongID = 0x10000001
	f.Units[0].Assistant = assistant.Compact(img)
	return f
}

func fileArea(t *testing.T, path string) []byte {
	t.Helper()
	r, err := loadRVSC(path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := r.units[0].AssistantImage()
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// TestAssistantsBackupRestore backs up a simulated unit with assistants
// into a copy of a backup file and restores the copy to another simulated
// unit with the same long ID and an empty area: the area is the same (#81
// acceptance 1). Without --yes nothing is written.
func TestAssistantsBackupRestore(t *testing.T) {
	prog := simulator.ProgramArea(900)
	last := assistantBus(t, prog, factory)
	in := writeSpec(t, "backup.rvsc", backupSpec(simulator.ErasedArea()))
	out := filepath.Join(t.TempDir(), "with-assistants.rvsc")
	code, stdout, errOut := cli(t, args("assistants", "read", in, "-o", out)...)
	if code != exitOK {
		t.Fatalf("read: exit %d: %s", code, errOut)
	}
	wantContains(t, stdout, "unit 10000001 at address 0", "906 bytes used, 1906 free", "assistant IDs stored: 8737, 9251, 9765",
		"running: 5 (ESS)", "wrote "+out)
	if !bytes.Equal(fileArea(t, out), prog) || !bytes.Equal(fileArea(t, in), simulator.ErasedArea()) {
		t.Fatal("the copy does not hold the area read, or the backup changed")
	}
	raw := filepath.Join(t.TempDir(), "area.bin")
	if code, _, errOut := cli(t, args("assistants", "read", "-o", raw)...); code != exitOK {
		t.Fatalf("read raw: exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(raw); !bytes.Equal(b, prog) {
		t.Error("the raw image differs from the area")
	}

	// Another simulated unit with the same long ID and an empty area.
	last = assistantBus(t, simulator.ErasedArea(), factory)
	code, stdout, errOut = cli(t, args("assistants", "write", out)...)
	if code != exitOK {
		t.Fatalf("dry run: exit %d: %s", code, errOut)
	}
	wantContains(t, stdout, "unit's area before: 2816 bytes, checksum ok, no assistants", "dry run: nothing written; with --yes 44 blocks")
	if img, writes := area(t, *last); writes != 0 || !bytes.Equal(img, simulator.ErasedArea()) {
		t.Fatalf("dry run wrote %d blocks", writes)
	}
	code, stdout, errOut = cli(t, args("assistants", "write", "--yes", out)...)
	if code != exitOK {
		t.Fatalf("--yes: exit %d: %s\n%s", code, stdout, errOut)
	}
	wantContains(t, stdout, "44 block write(s) sent", "the units were restarted", "read back and verified")
	if img, _ := area(t, *last); !bytes.Equal(img, prog) {
		t.Error("the restored area differs from the backup")
	}
	// A unit that already holds the area is not written.
	last = assistantBus(t, prog, factory)
	code, stdout, _ = cli(t, args("assistants", "write", "--yes", out)...)
	if _, writes := area(t, *last); code != exitOK || writes != 0 || !strings.Contains(stdout, "already holds this area") {
		t.Errorf("same area: exit %d, %d writes\n%s", code, writes, stdout)
	}
}

func TestAssistantsClear(t *testing.T) {
	last := assistantBus(t, simulator.ProgramArea(900), factory)
	code, stdout, errOut := cli(t, args("assistants", "clear")...)
	if _, writes := area(t, *last); code != exitOK || writes != 0 {
		t.Fatalf("dry run: exit %d, %d writes: %s", code, writes, errOut)
	}
	wantContains(t, stdout, "new area: 2816 bytes, checksum ok, no assistants: 0 bytes used, 2812 free", "dry run")
	if code, _, errOut := cli(t, args("assistants", "clear", "--yes")...); code != exitOK {
		t.Fatalf("--yes: exit %d: %s", code, errOut)
	}
	if img, _ := area(t, *last); !bytes.Equal(img, simulator.ErasedArea()) {
		t.Error("the area is not erased")
	}
}

// TestAssistantsRefusals: a file of another unit, and a unit not
// configured for assistants, are refused before anything is written.
func TestAssistantsRefusals(t *testing.T) {
	last := assistantBus(t, simulator.ErasedArea(), factory)
	spec := backupSpec(simulator.ProgramArea(900))
	spec.Units[0].LongID = 0x10000009
	other := writeSpec(t, "other.rvsc", spec)
	code, _, errOut := cli(t, args("assistants", "write", "--yes", other)...)
	if _, writes := area(t, *last); code != exitError || writes != 0 {
		t.Errorf("other unit: exit %d, %d writes", code, writes)
	}
	wantContains(t, errOut, "no unit with long ID 10000009 answered", "Nothing was written")

	last = assistantBus(t, simulator.ErasedArea(), [mk2.SystemRecordLen]byte{0x01, 0x01, 0x01, 0, 0, 0, 0, 0xF0, 0})
	mine := writeSpec(t, "mine.rvsc", backupSpec(simulator.ProgramArea(900)))
	code, _, errOut = cli(t, args("assistants", "write", "--yes", mine)...)
	if _, writes := area(t, *last); code != exitError || writes != 0 {
		t.Errorf("not configured: exit %d, %d writes", code, writes)
	}
	wantContains(t, errOut, "not configured for assistants", "Nothing was written.")
}

// TestAssistantsHardwareGate: on a serial port the commands stop with the
// reason before the port is opened.
func TestAssistantsHardwareGate(t *testing.T) {
	p := writeSpec(t, "a.rvsc", backupSpec(simulator.ProgramArea(900)))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"assistants", "write", "--port", "NOPORT", p}, "writing assistants to real devices is off until the hardware checks pass (#3). Nothing was sent"},
		{[]string{"assistants", "clear", "--port", "NOPORT", "--yes"}, "writing assistants to real devices is off"},
		{[]string{"assistants", "read", "--port", "NOPORT"}, "reading assistants from real devices is not available yet"},
		{[]string{"rvsc-apply", "--port", "NOPORT", "--assistants=write", "--yes", p}, "writing assistants to real devices is off"},
	} {
		code, _, errOut := cli(t, tc.args...)
		if code != exitError || !strings.Contains(errOut, tc.want) || strings.Contains(errOut, "NOPORT") {
			t.Errorf("%v: exit %d: %s", tc.args, code, errOut)
		}
	}
}

// TestRVSCApplyAssistants: rvsc-apply keeps the device's assistants by
// default; --assistants=write writes the file's area after the settings,
// with the checks before anything is written and a refusal when the unit is
// another one.
func TestRVSCApplyAssistants(t *testing.T) {
	prog := simulator.ProgramArea(900)
	last := assistantBus(t, simulator.ErasedArea(), factory)
	spec := backupSpec(prog)
	spec.Units[0].Settings[2] = 1460
	p := writeSpec(t, "a.rvsc", spec)

	code, stdout, errOut := cli(t, args("rvsc-apply", "--yes", p)...)
	if code != exitOK {
		t.Fatalf("keep: exit %d: %s\n%s", code, stdout, errOut)
	}
	wantContains(t, stdout, "all writes verified", "assistants: kept as the device has them")
	if _, writes := area(t, *last); writes != 0 {
		t.Fatalf("keep: %d block writes", writes)
	}

	spec.Units[0].Settings[2] = 1470
	p = writeSpec(t, "b.rvsc", spec)
	code, stdout, errOut = cli(t, args("rvsc-apply", "--assistants=write", p)...)
	if code != exitOK {
		t.Fatalf("write, dry run: exit %d: %s\n%s", code, stdout, errOut)
	}
	wantContains(t, stdout, "assistants: the file's area: 2816 bytes, checksum ok, 906 bytes used",
		"assistants: the checks passed; 44 blocks to write", "dry run: nothing was written", "assistants: dry run")
	if _, writes := area(t, *last); writes != 0 {
		t.Fatalf("dry run: %d block writes", writes)
	}

	code, stdout, errOut = cli(t, args("rvsc-apply", "--assistants=write", "--yes", p)...)
	if code != exitOK {
		t.Fatalf("write: exit %d: %s\n%s", code, stdout, errOut)
	}
	wantContains(t, stdout, "verified  setting 2 (UBatAbsorption)", "44 block write(s) sent", "read back and verified")
	if img, _ := area(t, *last); !bytes.Equal(img, prog) {
		t.Error("the file's area was not written")
	}

	spec.Units[0].LongID = 0x10000009
	p = writeSpec(t, "c.rvsc", spec)
	code, _, errOut = cli(t, args("rvsc-apply", "--assistants=write", "--yes", "--verbose", p)...)
	if code != exitError || writeFrames(errOut) != 0 || !strings.Contains(errOut, "refusing to apply the file: no unit with long ID 10000009") {
		t.Errorf("other unit: exit %d, %d writes", code, writeFrames(errOut))
	}
	if code, _, _ := cli(t, args("rvsc-apply", "--assistants=maybe", p)...); code != exitUsage {
		t.Errorf("--assistants=maybe: exit %d", code)
	}
}
