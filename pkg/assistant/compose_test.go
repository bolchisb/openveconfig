package assistant

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
)

// Synthetic system and user assistants, written for these tests.
const (
	synthStart = `
 .GLOBAL True
JMP .MACRO a
 .IF a <= 0xFF
  DATA 0x55, a, 0x64
 .ELSE
  DATA 0x54, a, a >> 8, 0x64
 .ENDIF
 .ENDM
 .GLOBAL False
 DATA 0xA1, 0xA2, 0xA3
`
	synthUser = `
Level .AID Level, EL, '2N 100 900 500'
 .AUE Level, lvl
lvl .ALD RB2($+1)
 DATA 0x44, lvl, lvl >> 8
 .IF big
n .SET 0
  .WHILE n < 1000
   DATA 0, 0, 0
n .SET n + 1
  .ENDW
 .ENDIF
`
	synthStop = " JMP 0\n"
)

func synthComposer(big bool) *Composer {
	return &Composer{
		Start:  Assistant{Name: "asfStart", ID: 0x0015, Source: []byte(synthStart)},
		Stop:   Assistant{Name: "asfStop", ID: 0x000E, Source: []byte(synthStop)},
		Users:  []Assistant{{Name: "User", ID: 0x0190, Source: []byte(synthUser)}},
		Labels: map[string]asm.Value{"big": asm.BoolVal(big)},
	}
}

func TestComposeLayout(t *testing.T) {
	c := synthComposer(false)
	ans := wizard.Answers{{Source: "User", Step: "level"}: {asm.IntVal(0x1234)}}
	im, err := c.Build(ans)
	if err != nil {
		t.Fatal(err)
	}
	img := im.Bytes
	prog := []byte{0xA1, 0xA2, 0xA3, 0x44, 0x34, 0x12, 0x55, 0x00, 0x64}
	if len(img) != ImageSize || !bytes.Equal(img[:len(prog)], prog) {
		t.Fatalf("program % x", img[:16])
	}
	for i := len(prog); i < 0xAF6; i++ {
		if img[i] != 0xFF {
			t.Fatalf("free space byte %#x = %#x", i, img[i])
		}
	}
	if tail := img[0xAF6:0xAFC]; !bytes.Equal(tail, []byte{0x0E, 0x00, 0x90, 0x01, 0x15, 0x00}) {
		t.Errorf("tail % x", tail)
	}
	if !ChecksumOK(img) {
		t.Error("checksum")
	}
	if im.Used != len(prog)+6 || im.Free != Budget-im.Used || len(im.Sizes) != 1 || im.Sizes[0] != 3+2 {
		t.Errorf("used %d free %d sizes %v", im.Used, im.Free, im.Sizes)
	}

	l, err := ParseLayout(img)
	if err != nil {
		t.Fatal(err)
	}
	if l.Program != len(prog) || l.Used != im.Used || l.Free != im.Free || len(l.IDs) != 3 || l.IDs[1] != 0x0190 {
		t.Errorf("layout %+v", l)
	}

	got, _, err := c.ReadBack(img)
	if err != nil {
		t.Fatal(err)
	}
	if v := got[wizard.Key{Source: "User", Step: "level"}]; len(v) != 1 || v[0].I != 0x1234 {
		t.Errorf("read back %v", v)
	}
}

func TestComposeTooBig(t *testing.T) {
	im, err := synthComposer(true).Build(nil)
	if !errors.Is(err, ErrTooBig) || im != nil {
		t.Errorf("err = %v", err)
	}
}

func TestComposeTwoUsers(t *testing.T) {
	c := synthComposer(false)
	c.Users = append(c.Users, Assistant{Name: "Other", ID: 0x0101, Source: []byte(" DATA 7, 7\n")})
	im, err := c.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Tail in reverse program order (inferred for several users).
	want := []byte{0x0E, 0x00, 0x01, 0x01, 0x90, 0x01, 0x15, 0x00}
	if tail := im.Bytes[Budget-8 : Budget]; !bytes.Equal(tail, want) {
		t.Errorf("tail % x", tail)
	}
	if len(im.Sizes) != 2 || im.Sizes[0] != 5 || im.Sizes[1] != 4 {
		t.Errorf("sizes %v", im.Sizes)
	}
	// The default of the step was used.
	if v := binary.LittleEndian.Uint16(im.Bytes[4:]); v != 500 {
		t.Errorf("default level %d", v)
	}
}

func TestParseLayoutErrors(t *testing.T) {
	if _, err := ParseLayout(make([]byte, 10)); !errors.Is(err, ErrFormat) {
		t.Errorf("short: %v", err)
	}
	img := bytes.Repeat([]byte{0xFF}, ImageSize)
	l, err := ParseLayout(img)
	if err != nil || l.Program != 0 || len(l.IDs) != 0 || l.Free != Budget {
		t.Errorf("bare: %+v %v", l, err)
	}
	img[0] = 0
	if _, err := ParseLayout(img); !errors.Is(err, ErrFormat) {
		t.Errorf("bad checksum: %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(file string, id uint16, src string) {
		p := &vap.Package{Format: 1, ID: id, Members: []vap.Member{
			{Name: "x.vll", Data: []byte(src)}, {Name: "x.en", Data: []byte("#Level\r\nHow high\r\n")},
		}}
		if err := os.WriteFile(filepath.Join(dir, file), vap.Encode(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("asfStart_0013.vap", 0x13, "bad old version\n")
	write("asfStart_0015.vap", 0x15, synthStart)
	write("asfStop_000E.vap", 0x0E, synthStop)
	write("User_0190.vap", 0x0190, synthUser)
	c, err := Load(dir, "en", 0x0190)
	if err != nil {
		t.Fatal(err)
	}
	c.Labels = map[string]asm.Value{"big": asm.BoolVal(false)}
	if c.Start.ID != 0x15 || c.Users[0].Name != "User" {
		t.Errorf("composer %+v", c)
	}
	s := c.Program().NewSession(nil)
	st, err := s.Start()
	if err != nil || st == nil || st.Text != "How high" || st.Source != "User" {
		t.Fatalf("step %+v %v", st, err)
	}
	if _, err := Load(dir, "en", 0x0999); err == nil {
		t.Error("missing id accepted")
	}
	if _, err := Load(t.TempDir(), "en"); err == nil {
		t.Error("folder without system assistants accepted")
	}
}
