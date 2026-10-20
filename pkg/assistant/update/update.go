// Package update downloads the assistant packages from Victron's update
// server into a package folder, as VEConfigure's "Download latest
// assistants" does (#33). The project ships no packages: the user's own
// copy of OpenVeConfig fetches them from Victron, as VEConfigure would.
//
// What the server offers (reverse-engineered, #33; restated in our own
// words). InfoPath is a text file whose lines 1-10 are a header; from line
// 11 on every product has a record of 10 lines: its number, its name, a
// link, then lines that a record may use. In the record named Product the
// fifth line is "<package version> <path>": the version of the package set
// and the path of its bundle on the server.
//
// A bundle is a run of entries, integers little-endian:
//
//	entry := u16 nameLen | name | u32 time | u32 attr | u32 size | size bytes
//
// name is a file name relative to the package folder; one that ends in a
// backslash is a folder and has no more fields. time and attr are the
// file's DOS date and attributes; they are not used here. After writing
// the files, VEConfigure notes the package set in vap.IndexFile.
package update

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
)

const (
	// Server is Victron's update server. It answers over HTTPS as well as
	// the HTTP that VEConfigure uses; HTTPS is used here.
	Server = "https://updates.victronenergy.com"
	// InfoPath is the server's list of products.
	InfoPath = "/SoftwareInfo.txt"
	// Product is the name of the record that offers the packages.
	Product = "VEConfigure 3"
	// maxBundle bounds a download; the bundle was 2.4 MB in 2026.
	maxBundle = 64 << 20
)

// ErrFormat means the server's data does not have the expected layout.
var ErrFormat = errors.New("update: unexpected data from the update server")

// Release is the package set the server offers.
type Release struct {
	Version int64  // the package set's version, as in vap.IndexFile
	Path    string // the bundle's path on the server
	// Number is the record's own number, the VEConfigure version that
	// offers the set. It is written as the downloader in vap.IndexFile, as
	// VEConfigure writes its own version there.
	Number int64
}

// ParseInfo returns the release in the server's list of products.
func ParseInfo(b []byte) (Release, error) {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		lines = append(lines, strings.TrimSpace(sc.Text()))
	}
	for i := 10; i+4 < len(lines); i += 10 {
		if lines[i+1] != Product {
			continue
		}
		num, err := strconv.ParseInt(lines[i], 10, 64)
		if err != nil {
			return Release{}, fmt.Errorf("%w: product number %q", ErrFormat, lines[i])
		}
		f := strings.Fields(lines[i+4])
		if len(f) != 2 || !strings.HasPrefix(f[1], "/") {
			return Release{}, fmt.Errorf("%w: package line %q", ErrFormat, lines[i+4])
		}
		v, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			return Release{}, fmt.Errorf("%w: package version %q", ErrFormat, f[0])
		}
		return Release{Version: v, Path: f[1], Number: num}, nil
	}
	return Release{}, fmt.Errorf("%w: no record for %s", ErrFormat, Product)
}

// Latest asks server (Server, or a test server) which package set it
// offers.
func Latest(ctx context.Context, c *http.Client, server string) (Release, error) {
	b, err := get(ctx, c, server+InfoPath, nil)
	if err != nil {
		return Release{}, err
	}
	return ParseInfo(b)
}

// Fetch downloads the bundle of r from server. progress, if not nil, is
// called with the bytes read so far and the size (-1 when unknown).
func Fetch(ctx context.Context, c *http.Client, server string, r Release, progress func(done, total int64)) ([]byte, error) {
	return get(ctx, c, server+r.Path, progress)
}

func get(ctx context.Context, c *http.Client, url string, progress func(done, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: %s: %s", url, resp.Status)
	}
	var buf bytes.Buffer
	r := io.LimitReader(resp.Body, maxBundle+1)
	chunk := make([]byte, 64<<10)
	for {
		n, err := r.Read(chunk)
		buf.Write(chunk[:n])
		if progress != nil && n > 0 {
			progress(int64(buf.Len()), resp.ContentLength)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if buf.Len() > maxBundle {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrFormat, url, maxBundle)
	}
	return buf.Bytes(), nil
}

// File is a file of a bundle.
type File struct {
	Name string // a plain file name, no folder
	Data []byte
}

// Unpack returns the files of a bundle. It accepts only plain file names
// of packages (.vap, .dvap) whose contents decode, so that nothing a
// bundle holds can be written outside the package folder or be anything
// else; folder entries are skipped, as the bundles seen have none.
func Unpack(b []byte) ([]File, error) {
	var files []File
	seen := map[string]bool{}
	for p := 0; p < len(b); {
		if len(b)-p < 2 {
			return nil, fmt.Errorf("%w: entry at %d cut short", ErrFormat, p)
		}
		n := int(binary.LittleEndian.Uint16(b[p:]))
		p += 2
		if n == 0 || len(b)-p < n {
			return nil, fmt.Errorf("%w: entry name at %d", ErrFormat, p)
		}
		name := string(b[p : p+n])
		p += n
		if strings.HasSuffix(name, `\`) {
			continue
		}
		if len(b)-p < 12 {
			return nil, fmt.Errorf("%w: %s cut short", ErrFormat, name)
		}
		size := int(binary.LittleEndian.Uint32(b[p+8:]))
		p += 12
		if size > len(b)-p {
			return nil, fmt.Errorf("%w: %s cut short", ErrFormat, name)
		}
		data := b[p : p+size]
		p += size
		if err := checkName(name); err != nil {
			return nil, err
		}
		if _, err := vap.Parse(data); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if key := strings.ToLower(name); !seen[key] {
			seen[key] = true
			files = append(files, File{Name: name, Data: data})
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: the bundle holds no packages", ErrFormat)
	}
	return files, nil
}

// checkName accepts a plain package file name.
func checkName(name string) error {
	ext := strings.ToLower(path.Ext(name))
	if name != filepath.Base(name) || strings.ContainsAny(name, `/\:`) || name == "." || name == ".." ||
		(ext != ".vap" && ext != ".dvap") {
		return fmt.Errorf("%w: file name %q", ErrFormat, name)
	}
	return nil
}

// Install writes files into dir, replacing the files of the same name, and
// notes r in dir's vap.IndexFile. Each file is written to a new file and
// renamed over the old one, so a failure leaves whole files only.
func Install(dir string, files []File, r Release) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if err := checkName(f.Name); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, f.Name), f.Data); err != nil {
			return err
		}
	}
	index := fmt.Sprintf("packageversion=%d\r\nDownloadedBy=%d\r\n", r.Version, r.Number)
	return writeFile(filepath.Join(dir, vap.IndexFile), []byte(index))
}

// writeFile replaces path with data. VEConfigure writes its packages
// read-only, so an existing file is made writable first.
func writeFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err2 := f.Close(); err == nil {
		err = err2
	}
	if err == nil {
		if _, serr := os.Stat(path); serr == nil {
			err = os.Chmod(path, 0o644)
		}
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}
