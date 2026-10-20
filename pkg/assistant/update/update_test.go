package update

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
)

// info is a made-up product list with the layout of the server's.
func info(product, pkgLine string) string {
	lines := make([]string, 10, 30)
	lines[0] = "a header of ten lines"
	rec := func(fields ...string) {
		r := make([]string, 10)
		copy(r, fields)
		lines = append(lines, r...)
	}
	rec("100", "Some other tool", "/x/other.exe")
	rec("9000001", product, "/x/setup.exe", "nl", pkgLine)
	return strings.Join(lines, "\r\n") + "\r\n"
}

func TestParseInfo(t *testing.T) {
	r, err := ParseInfo([]byte(info(Product, "42 /x/packs.vfp")))
	if err != nil {
		t.Fatal(err)
	}
	if r != (Release{Version: 42, Path: "/x/packs.vfp", Number: 9000001}) {
		t.Errorf("release = %+v", r)
	}
	for _, bad := range []string{info("Another product", "42 /x/packs.vfp"), info(Product, ""), info(Product, "x /x/packs.vfp"), info(Product, "42 packs.vfp")} {
		if _, err := ParseInfo([]byte(bad)); !errors.Is(err, ErrFormat) {
			t.Errorf("ParseInfo(%q) err = %v, want ErrFormat", bad, err)
		}
	}
}

// pkg is a synthetic package with id.
func pkg(id uint16) []byte {
	return vap.Encode(&vap.Package{Format: 1, ID: id, Members: []vap.Member{{Name: "x.vll", Data: []byte("; source")}}})
}

// bundle builds a bundle of the named entries; a name ending in a
// backslash is a folder.
func bundle(entries map[string][]byte, order ...string) []byte {
	var b []byte
	for _, name := range order {
		b = binary.LittleEndian.AppendUint16(b, uint16(len(name)))
		b = append(b, name...)
		if strings.HasSuffix(name, `\`) {
			continue
		}
		b = binary.LittleEndian.AppendUint32(b, 0x5A5A0000)
		b = binary.LittleEndian.AppendUint32(b, 0x21)
		b = binary.LittleEndian.AppendUint32(b, uint32(len(entries[name])))
		b = append(b, entries[name]...)
	}
	return b
}

func TestUnpack(t *testing.T) {
	e := map[string][]byte{"A_0001.vap": pkg(1), "B_0002.dvap": pkg(2)}
	files, err := Unpack(bundle(e, `sub\`, "A_0001.vap", "B_0002.dvap"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "A_0001.vap" || !bytes.Equal(files[1].Data, e["B_0002.dvap"]) {
		t.Errorf("files = %v", files)
	}
	for _, name := range []string{`..\evil.vap`, "../evil.vap", `C:\evil.vap`, "dir/evil.vap", "readme.txt", "evil.exe"} {
		if _, err := Unpack(bundle(map[string][]byte{name: pkg(1)}, name)); !errors.Is(err, ErrFormat) {
			t.Errorf("Unpack with %q: err = %v, want ErrFormat", name, err)
		}
	}
	whole := bundle(e, "A_0001.vap")
	if _, err := Unpack(whole[:len(whole)-1]); !errors.Is(err, ErrFormat) {
		t.Errorf("cut bundle: err = %v, want ErrFormat", err)
	}
	if _, err := Unpack(bundle(map[string][]byte{"A_0001.vap": []byte("not a package")}, "A_0001.vap")); err == nil {
		t.Error("a bundle with a corrupt package was accepted")
	}
	if _, err := Unpack(nil); !errors.Is(err, ErrFormat) {
		t.Errorf("empty bundle: err = %v, want ErrFormat", err)
	}
}

func TestInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Assistants")
	os.MkdirAll(dir, 0o755)
	old := filepath.Join(dir, "A_0001.vap")
	os.WriteFile(old, []byte("old"), 0o444) // VEConfigure leaves them read-only
	keep := filepath.Join(dir, "Mine_0009.vap")
	os.WriteFile(keep, []byte("mine"), 0o644)

	files := []File{{Name: "A_0001.vap", Data: pkg(1)}, {Name: "B_0002.vap", Data: pkg(2)}}
	if err := Install(dir, files, Release{Version: 117, Number: 9000001}); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if b, _ := os.ReadFile(filepath.Join(dir, f.Name)); !bytes.Equal(b, f.Data) {
			t.Errorf("%s not written", f.Name)
		}
	}
	if b, _ := os.ReadFile(keep); string(b) != "mine" {
		t.Error("a file that is not in the bundle was changed")
	}
	v, by, err := vap.ReadIndex(dir)
	if err != nil || v != 117 || by != 9000001 {
		t.Errorf("index = %d, %d, %v", v, by, err)
	}
	if err := Install(dir, []File{{Name: "../x.vap", Data: pkg(1)}}, Release{}); err == nil {
		t.Error("a file name with a folder was installed")
	}
}

func TestLatestAndFetch(t *testing.T) {
	b := bundle(map[string][]byte{"A_0001.vap": pkg(1)}, "A_0001.vap")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case InfoPath:
			w.Write([]byte(info(Product, "7 /x/packs.vfp")))
		case "/x/packs.vfp":
			w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	r, err := Latest(ctx, srv.Client(), srv.URL)
	if err != nil || r.Version != 7 {
		t.Fatalf("Latest = %+v, %v", r, err)
	}
	var last int64
	got, err := Fetch(ctx, srv.Client(), srv.URL, r, func(done, total int64) { last = done })
	if err != nil || !bytes.Equal(got, b) || last != int64(len(b)) {
		t.Errorf("Fetch = %d bytes, %v, progress %d", len(got), err, last)
	}
	if _, err := Fetch(ctx, srv.Client(), srv.URL, Release{Path: "/missing"}, nil); err == nil {
		t.Error("a missing bundle was fetched")
	}
}

// TestRealBundle unpacks a bundle downloaded from the server, named by
// ASSISTANT_BUNDLE (never committed).
func TestRealBundle(t *testing.T) {
	p := os.Getenv("ASSISTANT_BUNDLE")
	if p == "" {
		t.Skip("ASSISTANT_BUNDLE not set")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Unpack(b)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d packages", len(files))
}
