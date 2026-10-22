package assistant

import (
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/assistant/wizard"
)

// The layout of an image with assistants (reverse-engineered, #8):
//
//	0               program: the code of all assistants, linked
//	…               free space, 0xFF
//	Budget - 2n     tail: the id of each of the n assistants, u16 LE
//	Budget          checksum (Checksum), u32 LE
//
// The program starts with the hidden system assistant asfStart (whose
// first bytes are the 6 bytes every image seen starts with), then the
// user's assistants, then the hidden asfStop. The tail lists the ids in
// the reverse order: asfStop first, asfStart last (seen with one user
// assistant; the order of several is inferred). "Used" is the program
// plus the tail, as VEConfigure shows it.
const (
	// Budget is the room for the program and the tail: the image less
	// the checksum.
	Budget = ImageSize - 4
	// StartName and StopName are the system assistants, by package name.
	StartName = "asfStart"
	StopName  = "asfStop"
)

// ErrTooBig means the assistants do not fit in the image.
var ErrTooBig = errors.New("assistant: the assistants do not fit in the unit's assistant memory")

// Assistant is one assistant to place in an image.
type Assistant struct {
	Name   string // as sourced, and as Key.Source of its steps
	ID     uint16
	Source []byte
	Text   wizard.Text
}

// FromPackage returns the assistant in package p, named name, with its
// text in language lang ("en", …; none if the package has no such text).
func FromPackage(name string, p *vap.Package, lang string) (Assistant, error) {
	_, src, ok := p.Source()
	if !ok {
		return Assistant{}, fmt.Errorf("assistant: package %s has no source", name)
	}
	a := Assistant{Name: name, ID: p.ID, Source: src}
	if t, ok := p.Text(lang); ok {
		a.Text = wizard.ParseText(t)
	}
	return a, nil
}

// Composer combines assistants into an image.
type Composer struct {
	Start, Stop Assistant // the hidden system assistants
	Users       []Assistant
	// Labels is the environment the sources read: the unit's model,
	// firmware and settings (names in the spec: cMultiVersion,
	// cDeviceInfo, cIInvNominal, hwDC, hwAC, the bsi… layout labels,
	// <setting>_VEC with _scale/_offset/_minimum/_maximum/_default, …).
	// How the original derives each value is not known in full; the
	// caller supplies them.
	Labels map[string]asm.Value
}

// Load returns a composer with the newest asfStart and asfStop in dir and
// the user assistants with the given ids, in that order, with texts in
// language lang.
func Load(dir, lang string, ids ...uint16) (*Composer, error) {
	list, _, err := vap.List(dir)
	if err != nil {
		return nil, err
	}
	latest := vap.Latest(list)
	load := func(in vap.Info) (Assistant, error) {
		p, err := vap.Load(in.Path)
		if err != nil {
			return Assistant{}, err
		}
		return FromPackage(in.Name, p, lang)
	}
	c := &Composer{}
	for _, sys := range []struct {
		name string
		a    *Assistant
	}{{StartName, &c.Start}, {StopName, &c.Stop}} {
		in, ok := latest[sys.name]
		if !ok {
			return nil, fmt.Errorf("assistant: no %s package in %s", sys.name, dir)
		}
		if *sys.a, err = load(in); err != nil {
			return nil, err
		}
	}
	for _, id := range ids {
		var found *vap.Info
		for i := range list {
			if list[i].ID == id {
				found = &list[i]
			}
		}
		if found == nil {
			return nil, fmt.Errorf("assistant: no package with id %04X in %s", id, dir)
		}
		a, err := load(*found)
		if err != nil {
			return nil, err
		}
		c.Users = append(c.Users, a)
	}
	return c, nil
}

// all returns the assistants in program order.
func (c *Composer) all() []Assistant {
	return append(append([]Assistant{c.Start}, c.Users...), c.Stop)
}

// Program returns the combined source: each assistant sourced in program
// order, with a label at each boundary to measure its size.
func (c *Composer) Program() *wizard.Program {
	all := c.all()
	files := map[string][]byte{}
	texts := map[string]wizard.Text{}
	var main strings.Builder
	for i, a := range all {
		files[a.Name] = a.Source
		texts[a.Name] = a.Text
		fmt.Fprintf(&main, "%s\n .SOURCE %s\n", boundary(i), a.Name)
	}
	fmt.Fprintf(&main, "%s\n", boundary(len(all)))
	return &wizard.Program{
		Main:   "composer",
		Source: []byte(main.String()),
		Open: func(name string) ([]byte, error) {
			if b, ok := files[name]; ok {
				return b, nil
			}
			return nil, fmt.Errorf("assistant: no source %s", filepath.Base(name))
		},
		Labels: c.Labels,
		Texts:  texts,
	}
}

func boundary(i int) string { return fmt.Sprintf("$ComposerAt%d", i) }

// Image is a composed image.
type Image struct {
	Bytes []byte // ImageSize bytes
	// Sizes are the sizes of the user assistants as VEConfigure lists
	// them: code plus tail entry (inferred: it gives the size shown for
	// ESS with the unit seen).
	Sizes      []int
	Used, Free int
	Run        *wizard.Run
}

// Build assembles the assistants with the step values in answers
// (wizard.Session.Answers; steps without one take their defaults) and
// returns the image. It fails with ErrTooBig, before making an image,
// when the program and tail exceed Budget.
func (c *Composer) Build(answers wizard.Answers) (*Image, error) {
	run, err := c.Program().Assemble(wizard.Options{State: wizard.Build, Answers: answers})
	if err != nil {
		return nil, err
	}
	return c.image(run)
}

func (c *Composer) image(run *wizard.Run) (*Image, error) {
	all := c.all()
	at := make([]int, len(all)+1)
	for i := range at {
		v, ok := run.Result.Label(boundary(i))
		if !ok {
			return nil, fmt.Errorf("assistant: composer label %d lost", i)
		}
		at[i] = int(v.I)
	}
	n := len(run.Code)
	used := n + 2*len(all)
	if used > Budget {
		return nil, fmt.Errorf("%w: %d bytes needed, %d available", ErrTooBig, used, Budget)
	}
	img := make([]byte, ImageSize)
	copy(img, run.Code)
	for i := n; i < Budget; i++ {
		img[i] = 0xFF
	}
	tail := Budget - 2*len(all)
	for i, a := range all {
		binary.LittleEndian.PutUint16(img[tail+2*(len(all)-1-i):], a.ID)
	}
	binary.LittleEndian.PutUint32(img[Budget:], Checksum(img))
	im := &Image{Bytes: img, Used: used, Free: Budget - used, Run: run}
	for i := 1; i < len(all)-1; i++ {
		im.Sizes = append(im.Sizes, at[i+1]-at[i]+2)
	}
	return im, nil
}

// ReadBack assembles the assistants against img (a unit's image with the
// same assistants) and returns the step values recovered from it.
func (c *Composer) ReadBack(img []byte) (wizard.Answers, *wizard.Run, error) {
	return c.Program().ReadBack(img)
}

// Layout is what an image tells about itself without its sources.
type Layout struct {
	IDs     []uint16 // the tail, in image order (asfStop first)
	Program int      // program bytes
	Used    int
	Free    int
}

// Users returns the user assistants of the tail, in program order: the
// tail lists them backwards between asfStop and asfStart.
func (l *Layout) Users() []uint16 {
	if len(l.IDs) < 2 {
		return nil
	}
	ids := make([]uint16, 0, len(l.IDs)-2)
	for i := len(l.IDs) - 2; i >= 1; i-- {
		ids = append(ids, l.IDs[i])
	}
	return ids
}

// ParseLayout reads the tail and the program length of an image. The
// tail is the run of words before the checksum up to the free space; the
// program ends at the last byte before the free space that is not 0xFF
// (a program that ended in 0xFF bytes would look shorter; every program
// seen ends in a jump).
func ParseLayout(img []byte) (*Layout, error) {
	if len(img) != ImageSize {
		return nil, fmt.Errorf("%w: image of %d bytes, want %d", ErrFormat, len(img), ImageSize)
	}
	if !ChecksumOK(img) {
		return nil, fmt.Errorf("%w: image checksum", ErrFormat)
	}
	l := &Layout{}
	i := Budget
	for i >= 2 && binary.LittleEndian.Uint16(img[i-2:]) != 0xFFFF {
		i -= 2
		l.IDs = append([]uint16{binary.LittleEndian.Uint16(img[i:])}, l.IDs...)
	}
	p := i
	for p > 0 && img[p-1] == 0xFF {
		p--
	}
	l.Program = p
	l.Used = p + 2*len(l.IDs)
	l.Free = Budget - l.Used
	return l, nil
}
