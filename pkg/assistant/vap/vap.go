// Package vap reads assistant packages (.vap, and .dvap for private ones):
// the files in which VEConfigure keeps each assistant's source and its
// on-screen texts (#32). The project ships no packages; the user points it
// at the folder where VEConfigure downloaded them (DefaultDir).
//
// Layout (reverse-engineered, #8; restated in our own words). A package is
// one zlib stream (RFC 1950). Its decompressed bytes, integers
// little-endian:
//
//	package := u32 check | u16 32 | "Victron Energy Assistant Package" |
//	           u32 format | u32 id | u32 reserved | member*
//	member  := u16 nameLen | name | u32 memberCheck | u32 attr |
//	           u32 size | size bytes
//
// format is 1 in every package seen; id is the assistant identifier (low 16
// bits), the number VEConfigure lists and the composer writes into the
// image's tail. The decompressed bytes, read as little-endian u32 words
// (the last one zero-padded), sum to 0 modulo 2^32: check is chosen so.
// memberCheck and attr are kept but not interpreted (unknown).
//
// A package holds one assembler source (a member named *.vll) and one text
// file per language (a member named <title>.<lang>).
package vap

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Signature is the text after the check word of every package.
const Signature = "Victron Energy Assistant Package"

// DefaultDir is the folder into which VEConfigure downloads the packages on
// Windows (observed, #8). Other systems have no default; the user names the
// folder.
const DefaultDir = `C:\Users\Public\VE Configure tools\Assistants`

// Errors returned by this package wrap one of these.
var (
	// ErrFormat means the bytes do not follow the package layout.
	ErrFormat = errors.New("vap: malformed package")
	// ErrCorrupt means the integrity sum of the package is not 0.
	ErrCorrupt = errors.New("vap: package is corrupt")
)

// Member is one file inside a package.
type Member struct {
	Name  string
	Check uint32 // not interpreted
	Attr  uint32 // not interpreted; 0x20 on most members seen
	Data  []byte
}

// Package is a decoded package.
type Package struct {
	Format  uint32
	ID      uint16
	Members []Member
}

// Parse decodes a package file's bytes.
func Parse(b []byte) (*Package, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	d, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if sum(d) != 0 {
		return nil, ErrCorrupt
	}
	r := reader{b: d}
	r.u32() // check word, verified through the sum
	if r.str() != Signature {
		return nil, fmt.Errorf("%w: no signature", ErrFormat)
	}
	p := &Package{Format: r.u32(), ID: uint16(r.u32())}
	r.u32() // reserved
	for r.err == nil && r.pos < len(d) {
		m := Member{Name: r.str(), Check: r.u32(), Attr: r.u32()}
		m.Data = r.bytes(int(r.u32()))
		if r.err == nil {
			p.Members = append(p.Members, m)
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	return p, nil
}

// sum returns the sum of b as little-endian u32 words, the last one
// zero-padded.
func sum(b []byte) uint32 {
	var s uint32
	for i := 0; i < len(b); i += 4 {
		var w [4]byte
		copy(w[:], b[i:])
		s += binary.LittleEndian.Uint32(w[:])
	}
	return s
}

// Encode returns the package file of p, the inverse of Parse: it computes
// the check word and compresses. Tests use it to build synthetic packages.
func Encode(p *Package) []byte {
	var d []byte
	d = binary.LittleEndian.AppendUint32(d, 0)
	d = appendStr(d, Signature)
	d = binary.LittleEndian.AppendUint32(d, p.Format)
	d = binary.LittleEndian.AppendUint32(d, uint32(p.ID))
	d = binary.LittleEndian.AppendUint32(d, 0)
	for _, m := range p.Members {
		d = appendStr(d, m.Name)
		d = binary.LittleEndian.AppendUint32(d, m.Check)
		d = binary.LittleEndian.AppendUint32(d, m.Attr)
		d = binary.LittleEndian.AppendUint32(d, uint32(len(m.Data)))
		d = append(d, m.Data...)
	}
	binary.LittleEndian.PutUint32(d, -sum(d))
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	zw.Write(d)
	zw.Close()
	return out.Bytes()
}

func appendStr(d []byte, s string) []byte {
	d = binary.LittleEndian.AppendUint16(d, uint16(len(s)))
	return append(d, s...)
}

type reader struct {
	b   []byte
	pos int
	err error
}

func (r *reader) bytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.b)-r.pos < n {
		r.err = fmt.Errorf("%w: truncated at offset %d", ErrFormat, r.pos)
		return nil
	}
	b := r.b[r.pos : r.pos+n]
	r.pos += n
	return b
}

func (r *reader) u32() uint32 {
	b := r.bytes(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (r *reader) str() string {
	b := r.bytes(2)
	if b == nil {
		return ""
	}
	return string(r.bytes(int(binary.LittleEndian.Uint16(b))))
}

// Source returns the package's assembler source (its .vll member).
func (p *Package) Source() (name string, src []byte, ok bool) {
	for _, m := range p.Members {
		if strings.EqualFold(filepath.Ext(m.Name), ".vll") {
			return m.Name, m.Data, true
		}
	}
	return "", nil, false
}

// Text returns the package's text file for language lang ("en", "de", …).
func (p *Package) Text(lang string) ([]byte, bool) {
	for _, m := range p.Members {
		if strings.EqualFold(filepath.Ext(m.Name), "."+lang) {
			return m.Data, true
		}
	}
	return nil, false
}

// Info describes a package file in a folder.
type Info struct {
	Path string
	Name string // the assistant's name: the file name before "_<id>"
	ID   uint16
}

// List reads every .vap and .dvap file in dir and returns them sorted by
// name, then id. A file that does not decode is returned in bad with its
// error and left out of the list.
func List(dir string) (list []Info, bad map[string]error, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range ents {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if e.IsDir() || (ext != ".vap" && ext != ".dvap") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		p, err := Load(path)
		if err != nil {
			if bad == nil {
				bad = map[string]error{}
			}
			bad[path] = err
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if i := strings.LastIndexByte(name, '_'); i > 0 {
			name = name[:i]
		}
		list = append(list, Info{Path: path, Name: name, ID: p.ID})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].ID < list[j].ID
	})
	return list, bad, nil
}

// Load reads and decodes the package file at path.
func Load(path string) (*Package, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return p, nil
}

// Latest returns, for each assistant name in list, the package with the
// highest id.
func Latest(list []Info) map[string]Info {
	m := map[string]Info{}
	for _, in := range list {
		if old, ok := m[in.Name]; !ok || in.ID > old.ID {
			m[in.Name] = in
		}
	}
	return m
}

// IndexFile is the file in the package folder in which VEConfigure notes
// the package set it downloaded (observed, #8).
const IndexFile = "AssistantsInfo.txt"

// ReadIndex returns the package-set version and the downloader's version
// from dir's IndexFile ("packageversion=" and "DownloadedBy=" lines); a
// value that is missing is 0.
func ReadIndex(dir string) (packageVersion, downloadedBy int64, err error) {
	b, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		return 0, 0, err
	}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		n, nerr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if !ok || nerr != nil {
			continue
		}
		switch strings.ToLower(k) {
		case "packageversion":
			packageVersion = n
		case "downloadedby":
			downloadedBy = n
		}
	}
	return packageVersion, downloadedBy, nil
}
