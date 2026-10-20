package main

// The Assistants page (#33, in part), laid out as the original's
// Assistants tab: the tab Assistant Configuration with the group Assistant
// Setup (the file's assistants in program order, the bytes they use and
// the buttons that act on one) and the card of the unit's assistants
// (assistants.go), and the tab Assistant Tools (assistanttools.go). The
// tab, group and button labels are the original's words (docs/CLEAN-ROOM.md,
// short on-screen labels). Nothing on this page writes assistants into a
// file or a unit: adding, deleting, moving, saving and loading wait until
// the compiler (#32) builds the same code as VEConfigure. Start assistant
// and Summary read the file's values and write nothing
// (assistantwizard.go).

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// prefAssistantFolder is the preference that holds the package folder the
// user chose; empty means defaultAssistantDir.
const prefAssistantFolder = "assistantFolder"

// defaultAssistantDir is the package folder when the user chose none:
// VEConfigure's on Windows, one of the app's own elsewhere, where Download
// latest assistants puts them.
func defaultAssistantDir() string {
	if runtime.GOOS == "windows" {
		return vap.DefaultDir
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return vap.DefaultDir
	}
	return filepath.Join(dir, prefsDir, "Assistants")
}

// packageScan is what a package folder holds.
type packageScan struct {
	dir string
	err error // the folder cannot be read
	bad int   // files that are not packages that can be read
	// available are the user assistants, by title, each in its newest
	// version.
	available []availableAssistant
	titles    map[uint16]string // the title of every package, by ID
	paths     map[uint16]string // the file of every package, by ID
	// version is the package set noted in the folder (vap.ReadIndex); 0
	// when none is.
	version int64
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
	tabs *container.AppTabs
	scan *packageScan // nil while the folder is read
	// onScan are called on the UI goroutine after a folder was read.
	onScan []func()
	// ready is closed once the folder chosen last has been read and shown.
	ready  chan struct{}
	folder *widget.Label
	picker *assistantPicker // the open dialog Add assistant, or nil

	// The file's assistants in program order, the area they are in (nil
	// when the file has none whose layout is known), and the one selected
	// (-1 for none).
	installed []uint16
	img       []byte
	selected  int
	list      *widget.List
	empty     *widget.Label // over the list while it is empty
	used      *widget.Label
	bar       *widget.ProgressBar
	// start and summary act on the selected assistant; the other buttons
	// of the group are never enabled yet.
	start, summary *widget.Button

	tools *assistantTools
}

// assistantsPageOf returns the Assistants page of u, and sets
// u.assistants.
func assistantsPageOf(u *ui) *page {
	ap := &assistantsPage{u: u, folder: widget.NewLabel(""), selected: -1}
	u.assistants = ap
	ap.folder.Wrapping = fyne.TextWrapBreak
	ap.tools = newAssistantTools(ap)
	ap.tabs = container.NewAppTabs(
		container.NewTabItem(tr("Assistant Configuration"), newForm(ap.setupCard(), assistantsCard(u, ap))),
		container.NewTabItem(tr("Assistant Tools"), newForm(ap.tools.card(), ap.tools.folderCard())))
	pg := newPage(tr("Assistants"), tr("The assistants of the file and the unit, and the tools for the assistant packages."),
		theme.StorageIcon(), vebus.TabNone, ap.tabs)
	ap.onScan = append(ap.onScan, ap.refreshPicker, ap.showInstalled, ap.tools.showFolder)
	u.settings.onChange(func(bool) { ap.showInstalled() })
	dir := u.app.Preferences().String(prefAssistantFolder)
	if dir == "" {
		dir = defaultAssistantDir()
	}
	ap.setDir(dir)
	ap.tools.checkAtStart()
	return pg
}

// setDir reads the package folder dir, on another goroutine.
func (ap *assistantsPage) setDir(dir string) {
	ap.scan = nil
	ready := make(chan struct{})
	ap.ready = ready
	ap.folder.SetText(dir)
	for _, f := range ap.onScan {
		f()
	}
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
	s := &packageScan{dir: dir, titles: map[uint16]string{}, paths: map[uint16]string{}}
	list, bad, err := vap.List(dir)
	if err != nil {
		s.err = err
		return s
	}
	s.bad = len(bad)
	s.version, _, _ = vap.ReadIndex(dir)
	versions := map[string]int{}
	for _, in := range list {
		versions[in.Name]++
		s.paths[in.ID] = in.Path
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

// setupCard is the group Assistant Setup: Add assistant, the file's
// assistants with the arrows that move one, the bytes used, and the
// buttons that act on the selected assistant.
func (ap *assistantsPage) setupCard() fyne.CanvasObject {
	add := widget.NewButtonWithIcon(tr("Add assistant"), theme.ContentAddIcon(), ap.showAdd)
	ap.list = widget.NewList(func() int { return len(ap.installed) },
		func() fyne.CanvasObject {
			name := widget.NewLabel("")
			name.Truncation = fyne.TextTruncateEllipsis
			return container.NewBorder(nil, nil, widget.NewIcon(theme.StorageIcon()), newPill("", theme.ColorNamePrimary, false), name)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			c := o.(*fyne.Container)
			c.Objects[0].(*widget.Label).SetText(ap.title(ap.installed[i]))
			c.Objects[2].(*pill).setText(assistantID(ap.installed[i]))
		})
	ap.list.OnSelected = func(i widget.ListItemID) { ap.selected = i; ap.updateButtons() }
	ap.list.OnUnselected = func(widget.ListItemID) { ap.selected = -1; ap.updateButtons() }
	ap.empty = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{})
	ap.empty.Wrapping = fyne.TextWrapWord
	listArea := container.NewStack(newThemeRect(colorNamePage, theme.ColorNameSeparator, -1, fyne.NewSize(0, 200)),
		ap.list, container.NewCenter(ap.empty))

	// Moving an assistant changes the order the unit runs them in; it
	// waits for the compiler, as adding does.
	up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), nil)
	down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), nil)
	up.Disable()
	down.Disable()
	arrows := container.NewBorder(up, down, nil, nil)

	ap.used = widget.NewLabel("")
	ap.bar = widget.NewProgressBar()
	ap.bar.Max = assistant.Budget
	ap.bar.TextFormatter = func() string { return "" } // the label above says it

	ap.start = widget.NewButtonWithIcon(tr("Start assistant"), theme.MediaPlayIcon(), func() { ap.openSelected(false) })
	ap.summary = widget.NewButtonWithIcon(tr("Summary"), theme.ListIcon(), func() { ap.openSelected(true) })
	later := func(label string, icon fyne.Resource) *widget.Button {
		b := widget.NewButtonWithIcon(label, icon, nil)
		b.Disable()
		return b
	}
	buttons := container.NewGridWithColumns(3,
		ap.start, later(tr("Save assistant"), theme.DocumentSaveIcon()), later(tr("Delete assistant"), theme.DeleteIcon()),
		ap.summary, later(tr("Load assistant"), theme.FolderOpenIcon()))
	note := mutedText(tr("Start assistant walks through the selected assistant with the file's values, and Summary lists them; "+
		"neither writes anything. Adding, deleting, moving, saving and loading assistants come once {{.App}} builds "+
		"the same assistant code as VEConfigure.", args{"App": appName}))

	ap.showInstalled()
	return newFormCard(cardTitle(tr("Assistant Setup")),
		container.NewHBox(add),
		container.NewBorder(nil, nil, nil, arrows, listArea),
		ap.used, container.New(barHeight(8), ap.bar), buttons, note)
}

// barHeight lays its objects out at its height, their full width and
// centred, so that a progress bar without text is a thin bar.
type barHeight float32

func (h barHeight) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(0, (size.Height-float32(h))/2))
		o.Resize(fyne.NewSize(size.Width, float32(h)))
	}
}

func (h barHeight) MinSize(objs []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(0, float32(h))
}

// showInstalled lists the assistants of the open file.
func (ap *assistantsPage) showInstalled() {
	if ap.list == nil {
		return // the card is not built yet
	}
	ap.installed, ap.img = nil, nil
	empty := tr("No settings file is open.")
	used, free := 0, assistant.Budget
	if sf := ap.u.settings.file; sf != nil {
		img, err := sf.unit.AssistantImage()
		l, lerr := assistant.ParseLayout(img)
		switch {
		case err != nil:
			empty = fileAreaSummary(sf.unit)
		case lerr != nil:
			empty = areaSummary(img)
		default:
			ap.installed, ap.img = userIDs(l.IDs), img
			used, free = l.Used, l.Free
			empty = tr("No assistants")
		}
	}
	ap.empty.SetText(empty)
	if len(ap.installed) == 0 {
		ap.empty.Show()
	} else {
		ap.empty.Hide()
	}
	// The original's words.
	ap.used.SetText(tr("Used assistants: ({{.Used}} bytes used, {{.Free}} bytes free)",
		args{"Used": formatNumber(float64(used), 0), "Free": formatNumber(float64(free), 0)}))
	ap.bar.SetValue(float64(used))
	if ap.selected >= len(ap.installed) {
		ap.list.UnselectAll()
		ap.selected = -1
	}
	ap.list.Refresh()
	ap.updateButtons()
}

// updateButtons enables the buttons that act on the selected assistant
// once the package folder has been read.
func (ap *assistantsPage) updateButtons() {
	if ap.selected >= 0 && ap.selected < len(ap.installed) && ap.scan != nil {
		ap.start.Enable()
		ap.summary.Enable()
	} else {
		ap.start.Disable()
		ap.summary.Disable()
	}
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

// refreshPicker shows the folder read last in the open dialog Add
// assistant, and enables the buttons that need the folder.
func (ap *assistantsPage) refreshPicker() {
	if ap.picker != nil {
		ap.picker.refresh()
	}
	ap.updateButtons()
}

// openSelected opens the selected assistant of the file: its steps with
// the file's values (Start assistant), or the summary of those values.
func (ap *assistantsPage) openSelected(summary bool) {
	i := ap.selected
	if i < 0 || i >= len(ap.installed) || ap.scan == nil {
		return
	}
	ids, img, s := slices.Clone(ap.installed), ap.img, ap.scan
	title := ap.title(ids[i])
	lang := "en"
	if p, err := vap.Load(s.paths[ids[i]]); err == nil {
		lang = packageLanguage(p)
	}
	runWithProgress(ap.u, tr("Opening the assistant"), func(context.Context, func(done, total int)) error {
		c, err := assistant.Load(s.dir, lang, ids...)
		if err != nil {
			fyne.Do(func() { (&assistantWizard{u: ap.u, title: title, n: 1}).show(nil, err) })
			return nil
		}
		c.Labels = assistantLabels(ap.u, s.dir)
		answers, run, rerr := c.ReadBack(img)
		name := c.Users[i].Name
		if summary {
			fyne.Do(func() { showInstalledSummary(ap.u, title, name, run, rerr) })
			return nil
		}
		sess := c.Program().NewSession(answers)
		sess.Edit = name
		st, err := sess.Start()
		fyne.Do(func() {
			w := &assistantWizard{u: ap.u, title: title, sess: sess, n: 1, apply: tr("Save in the file"), readErr: rerr}
			w.show(st, err)
		})
		return nil
	})
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
