package vap

import (
	"bytes"
	"compress/zlib"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// synthetic is a package built here; its contents are made up.
func synthetic() *Package {
	return &Package{Format: 1, ID: 0x0123, Members: []Member{
		{Name: "Test.en", Attr: 0x20, Data: []byte("#Intro\r\nhello\r\n")},
		{Name: "Test.vll", Attr: 0x20, Data: []byte(" DATA 1\r\n")},
	}}
}

func TestRoundTrip(t *testing.T) {
	b := Encode(synthetic())
	if b[0] != 0x78 {
		t.Fatalf("not a zlib stream: % x", b[:2])
	}
	p, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Format != 1 || p.ID != 0x0123 || len(p.Members) != 2 {
		t.Fatalf("got %+v", p)
	}
	name, src, ok := p.Source()
	if !ok || name != "Test.vll" || string(src) != " DATA 1\r\n" {
		t.Errorf("Source = %q %q %v", name, src, ok)
	}
	if txt, ok := p.Text("en"); !ok || !bytes.HasPrefix(txt, []byte("#Intro")) {
		t.Errorf("Text(en) = %q %v", txt, ok)
	}
	if _, ok := p.Text("de"); ok {
		t.Error("Text(de) found")
	}
}

func TestSumOfWords(t *testing.T) {
	// 5 bytes: one word and one zero-padded byte.
	if got := sum([]byte{1, 0, 0, 0, 2}); got != 3 {
		t.Errorf("sum = %d", got)
	}
}

func compress(d []byte) []byte {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	zw.Write(d)
	zw.Close()
	return out.Bytes()
}

func TestErrors(t *testing.T) {
	if _, err := Parse([]byte("not zlib")); !errors.Is(err, ErrFormat) {
		t.Errorf("garbage: %v", err)
	}
	// Flip one byte inside the decompressed data: the sum is no longer 0.
	zr, _ := zlib.NewReader(bytes.NewReader(Encode(synthetic())))
	var d bytes.Buffer
	d.ReadFrom(zr)
	raw := d.Bytes()
	raw[len(raw)-1] ^= 1
	if _, err := Parse(compress(raw)); !errors.Is(err, ErrCorrupt) {
		t.Errorf("flipped byte: %v", err)
	}
	// A truncated member, with the check word fixed up.
	p := synthetic()
	p.Members = nil
	zr, _ = zlib.NewReader(bytes.NewReader(Encode(p)))
	d.Reset()
	d.ReadFrom(zr)
	raw = append(d.Bytes(), 9, 0, 'x', 0, 0, 0) // nameLen 9, 1 name byte, padding
	raw[0], raw[1], raw[2], raw[3] = 0, 0, 0, 0
	s := -sum(raw)
	raw[0], raw[1], raw[2], raw[3] = byte(s), byte(s>>8), byte(s>>16), byte(s>>24)
	if _, err := Parse(compress(raw)); !errors.Is(err, ErrFormat) {
		t.Errorf("truncated: %v", err)
	}
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, id uint16) {
		p := synthetic()
		p.ID = id
		if err := os.WriteFile(filepath.Join(dir, name), Encode(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Test_0123.vap", 0x0123)
	write("Test_0125.vap", 0x0125)
	write("other thing_0001.dvap", 0x0001)
	os.WriteFile(filepath.Join(dir, "broken_0002.vap"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	list, bad, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != "Test" || list[0].ID != 0x0123 || list[2].Name != "other thing" {
		t.Errorf("list = %+v", list)
	}
	if len(bad) != 1 {
		t.Errorf("bad = %v", bad)
	}
	if l := Latest(list); l["Test"].ID != 0x0125 || len(l) != 2 {
		t.Errorf("Latest = %+v", l)
	}
}

// TestRealPackages decodes every package in $ASSISTANT_PACKAGES, if set.
// Packages are not committed (they are Victron's); it logs only names and
// ids.
func TestRealPackages(t *testing.T) {
	dir := os.Getenv("ASSISTANT_PACKAGES")
	if dir == "" {
		t.Skip("ASSISTANT_PACKAGES not set")
	}
	list, bad, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	for path, err := range bad {
		t.Errorf("%s: %v", path, err)
	}
	for _, in := range list {
		p, err := Load(in.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, ok := p.Source(); !ok {
			t.Errorf("%s: no source", filepath.Base(in.Path))
		}
	}
	t.Logf("%d packages, %d assistants", len(list), len(Latest(list)))
}

func TestReadIndex(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, IndexFile), []byte("packageversion=7\r\nDownloadedBy=123\r\nother=x\r\n"), 0o644)
	pv, by, err := ReadIndex(dir)
	if err != nil || pv != 7 || by != 123 {
		t.Errorf("ReadIndex = %d %d %v", pv, by, err)
	}
	if _, _, err := ReadIndex(t.TempDir()); err == nil {
		t.Error("missing index accepted")
	}
}
