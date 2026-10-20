package main

// The Assistants page (#33, in part): the assistants installed in the open
// file and in the unit (the card of assistants.go), the assistants
// available in the user's package folder, and a preview of an assistant's
// questions (assistantwizard.go). Nothing on this page writes assistants:
// adding, removing and reordering wait until the compiler (#32) builds the
// same code as VEConfigure.

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// prefAssistantFolder is the preference that holds the package folder the
// user chose; empty means vap.DefaultDir.
const prefAssistantFolder = "assistantFolder"

// packageScan is what a package folder holds.
type packageScan struct {
	dir string
	err error // the folder cannot be read
	bad int   // files that are not packages that can be read
	// available are the user assistants, by title, each in its newest
	// version.
	available []availableAssistant
	titles    map[uint16]string // the title of every package, by ID
}

// availableAssistant is a user assistant in the package folder.
type availableAssistant struct {
	info     vap.Info // the newest version
	title    string
	versions int
}

// assistantsPage holds the state of the Assistants page.
type assistantsPage struct {
	u    *ui
	scan *packageScan // nil while the folder is read
	// onScan are called on the UI goroutine after a folder was read.
	onScan []func()
	// ready is closed once the folder chosen last has been read and shown.
	ready     chan struct{}
	folder    *widget.Label
	available *fyne.Container
}

// assistantsPageOf returns the Assistants page of u, and sets
// u.assistants.
func assistantsPageOf(u *ui) *page {
	ap := &assistantsPage{u: u, folder: widget.NewLabel(""), available: container.NewVBox()}
	u.assistants = ap
	ap.folder.Wrapping = fyne.TextWrapBreak
	content := newForm(assistantsCard(u, ap), ap.availableCard())
	pg := newPage(tr("Assistants"), tr("The assistants of the file and the unit, and the assistants in your assistant packages."),
		theme.StorageIcon(), vebus.TabNone, content)
	ap.onScan = append(ap.onScan, ap.showAvailable)
	dir := u.app.Preferences().String(prefAssistantFolder)
	if dir == "" {
		dir = vap.DefaultDir
	}
	ap.setDir(dir)
	return pg
}

// setDir reads the package folder dir, on another goroutine.
func (ap *assistantsPage) setDir(dir string) {
	ap.scan = nil
	ready := make(chan struct{})
	ap.ready = ready
	ap.folder.SetText(dir)
	ap.showAvailable()
	go func() {
		s := scanPackages(dir)
		fyne.Do(func() {
			if ap.folder.Text != dir {
				return // another folder was chosen meanwhile
			}
			ap.scan = s
			for _, f := range ap.onScan {
				f()
			}
			close(ready)
		})
	}()
}

// title returns the title of the assistant with id: from its package, or
// one made of the ID.
func (ap *assistantsPage) title(id uint16) string {
	if ap.scan != nil && ap.scan.titles[id] != "" {
		return ap.scan.titles[id]
	}
	return tr("Assistant {{.ID}}", args{"ID": assistantID(id)})
}

// assistantID writes an assistant ID as the package file names do, in hex.
func assistantID(id uint16) string { return fmt.Sprintf("0x%04X", id) }

// scanPackages reads the packages in dir.
func scanPackages(dir string) *packageScan {
	s := &packageScan{dir: dir, titles: map[uint16]string{}}
	list, bad, err := vap.List(dir)
	if err != nil {
		s.err = err
		return s
	}
	s.bad = len(bad)
	versions := map[string]int{}
	for _, in := range list {
		versions[in.Name]++
		if p, err := vap.Load(in.Path); err == nil {
			s.titles[in.ID] = packageTitle(p, in.Name)
		}
	}
	for name, in := range vap.Latest(list) {
		if name == assistant.StartName || name == assistant.StopName {
			continue // the hidden system assistants
		}
		s.available = append(s.available, availableAssistant{info: in, title: s.titles[in.ID], versions: versions[name]})
	}
	slices.SortFunc(s.available, func(a, b availableAssistant) int {
		return strings.Compare(strings.ToLower(a.title), strings.ToLower(b.title))
	})
	return s
}

// packageLanguage returns the language of a package's texts to show: the
// UI language if the package has it, else English.
func packageLanguage(p *vap.Package) string {
	base, _ := uiLanguage.Base()
	if _, ok := p.Text(base.String()); ok {
		return base.String()
	}
	return "en"
}

// packageTitle returns the title of the assistant in p, in the language of
// packageLanguage, else name. The title is the name of the package's text
// file without its language extension; the names seen are Latin-1, with
// characters that cannot be in a file name written as %XX (observed in
// the user's packages).
func packageTitle(p *vap.Package, name string) string {
	ext := "." + packageLanguage(p)
	for _, m := range p.Members {
		if !strings.EqualFold(filepath.Ext(m.Name), ext) {
			continue
		}
		t := strings.TrimSuffix(m.Name, filepath.Ext(m.Name))
		if u, err := url.PathUnescape(t); err == nil {
			t = u
		}
		if !utf8.ValidString(t) {
			r := make([]rune, len(t))
			for i := range len(t) {
				r[i] = rune(t[i])
			}
			t = string(r)
		}
		if t = strings.TrimFunc(t, unicode.IsSpace); t != "" {
			return t
		}
	}
	return name
}

// availableCard is the card of the assistants in the package folder.
func (ap *assistantsPage) availableCard() fyne.CanvasObject {
	choose := widget.NewButtonWithIcon(tr("Choose folder…"), theme.FolderOpenIcon(), ap.chooseFolder)
	where := container.NewBorder(nil, nil, widget.NewLabelWithStyle(tr("Folder"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), choose, ap.folder)
	note := mutedText(tr("Assistant packages come from your own VEConfigure installation, which downloads them into this folder. "+
		"{{.App}} never ships them.", args{"App": appName}))
	later := callout(theme.InfoIcon(), tr("Preview walks through an assistant's questions and writes nothing. "+
		"Adding, removing and reordering assistants come once {{.App}} builds the same assistant code as VEConfigure.", args{"App": appName}))
	return newFormCard(cardTitle(tr("Available assistants")), where, note, ap.available, later)
}

// chooseFolder asks for the package folder and reads it.
func (ap *assistantsPage) chooseFolder() {
	showFolderOpen(ap.u.win, ap.folder.Text, func(l fyne.ListableURI, err error) {
		if err != nil || l == nil {
			return
		}
		ap.u.app.Preferences().SetString(prefAssistantFolder, l.Path())
		ap.setDir(l.Path())
	})
}

// showAvailable lists the assistants of the folder read last.
func (ap *assistantsPage) showAvailable() {
	s := ap.scan
	var rows []fyne.CanvasObject
	switch {
	case s == nil:
		rows = append(rows, mutedText(tr("Reading the folder…")))
	case s.err != nil:
		rows = append(rows, emptyState(theme.FolderIcon(), tr("No assistant folder"),
			tr("This folder cannot be read. Choose the folder into which VEConfigure downloaded its assistants.")))
	case len(s.available) == 0:
		rows = append(rows, emptyState(theme.FolderIcon(), tr("No assistant packages"),
			tr("This folder has no assistant packages. Choose the folder into which VEConfigure downloaded its assistants.")))
	default:
		for i, a := range s.available {
			if i > 0 {
				rows = append(rows, widget.NewSeparator())
			}
			rows = append(rows, ap.availableRow(a))
		}
	}
	if s != nil && s.bad > 0 {
		rows = append(rows, captionText(trn("{{.Count}} file in this folder is not an assistant package that can be read.",
			"{{.Count}} files in this folder are not assistant packages that can be read.", s.bad), theme.ColorNamePlaceHolder))
	}
	ap.available.Objects = rows
	ap.available.Refresh()
}

// availableRow is one assistant of the folder, with its preview button.
func (ap *assistantsPage) availableRow(a availableAssistant) fyne.CanvasObject {
	name := widget.NewLabelWithStyle(a.title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	name.Wrapping = fyne.TextWrapWord
	detail := captionText(trn("ID {{.ID}} · {{.Count}} version in the folder", "ID {{.ID}} · {{.Count}} versions in the folder",
		a.versions, args{"ID": assistantID(a.info.ID)}), theme.ColorNamePlaceHolder)
	preview := widget.NewButtonWithIcon(tr("Preview…"), theme.MediaPlayIcon(), func() { previewAssistant(ap.u, ap.scan.dir, a) })
	return container.NewBorder(nil, nil, nil, container.NewCenter(preview),
		container.New(layout.NewCustomPaddedVBoxLayout(0), name, detail))
}

// installedView shows the assistants in an image, by title, in program
// order, and the bytes used.
func installedView(img []byte, title func(uint16) string) fyne.CanvasObject {
	l, err := assistant.ParseLayout(img)
	if err != nil {
		t := widget.NewLabel(areaSummary(img))
		t.Wrapping = fyne.TextWrapWord
		return t
	}
	box := container.NewVBox()
	ids := userIDs(l.IDs)
	if len(ids) == 0 {
		box.Add(widget.NewLabel(tr("No assistants")))
	}
	for _, id := range ids {
		name := widget.NewLabel(title(id))
		name.Wrapping = fyne.TextWrapWord
		box.Add(container.NewBorder(nil, nil, widget.NewIcon(theme.NewThemedResource(theme.StorageIcon())),
			newPill(assistantID(id), theme.ColorNamePrimary, false), name))
	}
	bar := widget.NewProgressBar()
	bar.Max, bar.Value = float64(assistant.Budget), float64(l.Used)
	a := args{"Used": formatNumber(float64(l.Used), 0), "Size": formatNumber(assistant.Budget, 0), "Free": formatNumber(float64(l.Free), 0)}
	bar.TextFormatter = func() string { return tr("{{.Used}} of {{.Size}} bytes used · {{.Free}} free", a) }
	box.Add(bar)
	return box
}

// userIDs returns the user assistants of an image's tail, in program
// order: the tail lists them in reverse, between the system assistants
// asfStop (first) and asfStart (last) (compose.go).
func userIDs(tail []uint16) []uint16 {
	if len(tail) < 2 {
		return tail
	}
	var ids []uint16
	for i := len(tail) - 2; i >= 1; i-- {
		ids = append(ids, tail[i])
	}
	return ids
}
