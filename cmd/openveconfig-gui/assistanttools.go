package main

// The tab Assistant Tools of the Assistants page (#33), as the original's:
// Download latest assistants with its start-up check, Translate assistants
// (not available), Repair assistant installation, Manage private
// assistants and Error report; and the package folder, which the original
// does not show. The button and check box labels are the original's words
// (CONTRIBUTING.md, short on-screen labels). Downloads come from
// Victron's update server (pkg/assistant/update).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/assistant/update"
	"github.com/bolchisb/openveconfig/pkg/assistant/vap"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// prefAssistantAutoDownload is the preference of the start-up check. It is
// off until the user turns it on (ours: the original's is on), so that the
// app asks the internet for nothing unasked.
const prefAssistantAutoDownload = "assistants.autoDownload"

// assistantServer is the update server; tests use their own.
var assistantServer = update.Server

// assistantHTTP is the client of the update server.
var assistantHTTP = &http.Client{Timeout: 5 * time.Minute}

// privateFolder is the folder of the private assistants: PrivateAssistants
// next to the package folder (the name is the original's; the place is
// inferred, #33).
func privateFolder(dir string) string { return filepath.Join(filepath.Dir(dir), "PrivateAssistants") }

// assistantTools holds the state of the tab Assistant Tools.
type assistantTools struct {
	ap     *assistantsPage
	status *widget.RichText // what the package folder holds
}

func newAssistantTools(ap *assistantsPage) *assistantTools {
	return &assistantTools{ap: ap, status: mutedText("")}
}

// card is the original's tools, each with what it does.
func (t *assistantTools) card() fyne.CanvasObject {
	prefs := t.ap.u.app.Preferences()
	tool := func(b *widget.Button, what string) fyne.CanvasObject {
		return container.NewBorder(nil, nil, container.New(fixedWidth(270), b), nil, mutedText(what))
	}
	download := widget.NewButtonWithIcon(tr("Download latest assistants"), theme.DownloadIcon(), func() { t.download(false) })
	download.Importance = widget.HighImportance
	auto := widget.NewCheck(tr("Automatically download assistant updates at program startup"), nil)
	auto.SetChecked(prefs.Bool(prefAssistantAutoDownload))
	auto.OnChanged = func(on bool) { prefs.SetBool(prefAssistantAutoDownload, on) }
	translate := widget.NewButtonWithIcon(tr("Translate assistants"), theme.DocumentCreateIcon(), nil)
	translate.Disable()
	repair := widget.NewButtonWithIcon(tr("Repair assistant installation"), theme.ViewRefreshIcon(), func() { t.download(true) })
	private := widget.NewButtonWithIcon(tr("Manage private assistants"), theme.FolderNewIcon(), t.managePrivate)
	report := widget.NewButtonWithIcon(tr("Error report"), theme.DocumentIcon(), t.errorReport)
	return newFormCard(cardTitle(tr("Assistant packages")),
		tool(download, tr("Downloads the newest assistant packages from Victron Energy into the package folder.")),
		auto,
		widget.NewSeparator(),
		tool(repair, tr("Checks the packages in the folder and downloads the whole package set again.")),
		tool(private, tr("Adds and removes your own assistant packages.")),
		tool(translate, tr("For translators of Victron Energy's assistant texts; not available in {{.App}}.", args{"App": appName})),
		tool(report, tr("A report on the assistants and the package folder, to copy into a question for support.")))
}

// folderCard is the package folder and what it holds.
func (t *assistantTools) folderCard() fyne.CanvasObject {
	choose := widget.NewButtonWithIcon(tr("Choose folder…"), theme.FolderOpenIcon(), t.ap.chooseFolder)
	where := container.NewBorder(nil, nil, widget.NewLabelWithStyle(tr("Folder"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), choose, t.ap.folder)
	note := mutedText(tr("Assistant packages are Victron Energy's. {{.App}} never ships them: they come from your VEConfigure "+
		"installation, or Download latest assistants fetches them from Victron Energy.", args{"App": appName}))
	return newFormCard(cardTitle(tr("Package folder")), where, t.status, note)
}

// showFolder says what the package folder read last holds.
func (t *assistantTools) showFolder() {
	s := t.ap.scan
	var text string
	switch {
	case s == nil:
		text = tr("Reading the folder…")
	case s.err != nil:
		text = tr("This folder cannot be read. Choose the folder into which VEConfigure downloaded its assistants, or download them into it.")
	case s.version > 0:
		text = trn("{{.Count}} assistant package · package set {{.Version}}", "{{.Count}} assistant packages · package set {{.Version}}",
			len(s.paths), args{"Version": strconv.FormatInt(s.version, 10)})
	default:
		text = trn("{{.Count}} assistant package · no package set noted", "{{.Count}} assistant packages · no package set noted", len(s.paths))
	}
	if s != nil && s.err == nil && s.bad > 0 {
		text += " · " + trn("{{.Count}} file cannot be read", "{{.Count}} files cannot be read", s.bad)
	}
	t.status.Segments[0].(*widget.TextSegment).Text = text
	t.status.Refresh()
}

// download asks the server for its package set and offers to download it:
// when it is newer than the folder's, or always to repair the folder.
func (t *assistantTools) download(repair bool) {
	u, dir := t.ap.u, t.ap.folder.Text
	bad := 0
	if s := t.ap.scan; s != nil {
		bad = s.bad
	}
	runWithProgress(u, tr("Checking for new assistants"), func(ctx context.Context, _ func(done, total int)) error {
		r, err := update.Latest(ctx, assistantHTTP, assistantServer)
		if err != nil {
			return &uiError{err: err, msg: tr("Victron Energy's update server cannot be reached: {{.Err}}", args{"Err": err})}
		}
		have, _, _ := vap.ReadIndex(dir)
		fyne.Do(func() { t.offer(dir, r, have, repair, bad) })
		return nil
	})
}

// offer asks whether to download release r into dir, whose package set is
// have (0: none noted), or says that dir has it.
func (t *assistantTools) offer(dir string, r update.Release, have int64, repair bool, bad int) {
	u := t.ap.u
	a := args{"New": strconv.FormatInt(r.Version, 10), "Old": strconv.FormatInt(have, 10), "Folder": dir}
	download := func() { t.install(dir, r) }
	switch {
	case repair:
		msg := tr("Repair downloads package set {{.New}} again and replaces the packages of the same name in {{.Folder}}.", a)
		if bad > 0 {
			msg = trn("{{.Count}} file in the folder is not a package that can be read.",
				"{{.Count}} files in the folder are not packages that can be read.", bad) + " " + msg
		}
		confirm(u, tr("Repair assistant installation"), msg, tr("Repair"), download)
	case have >= r.Version:
		showMessage(u, tr("Download latest assistants"), tr("The folder has the latest assistants: package set {{.New}}.", a))
	case have == 0:
		confirm(u, tr("Download latest assistants"), tr("Package set {{.New}} is available. Download it into {{.Folder}}?", a), tr("Download"), download)
	default:
		confirm(u, tr("Download latest assistants"), tr("Package set {{.New}} is available; the folder has package set {{.Old}}. "+
			"Download it into {{.Folder}}?", a), tr("Download"), download)
	}
}

// install downloads release r into dir and reads the folder again.
func (t *assistantTools) install(dir string, r update.Release) {
	u := t.ap.u
	runWithProgress(u, tr("Downloading the assistants"), func(ctx context.Context, progress func(done, total int)) error {
		b, err := update.Fetch(ctx, assistantHTTP, assistantServer, r, func(done, total int64) {
			if total > 0 {
				progress(int(done), int(total))
			}
		})
		var files []update.File
		if err == nil {
			files, err = update.Unpack(b)
		}
		if err == nil {
			err = update.Install(dir, files, r)
		}
		if err != nil {
			return &uiError{err: err, msg: tr("The assistants could not be downloaded: {{.Err}}", args{"Err": err})}
		}
		fyne.Do(func() {
			t.ap.setDir(dir)
			showMessage(u, tr("Download latest assistants"), trn("Package set {{.Version}} is in the folder now: {{.Count}} package.",
				"Package set {{.Version}} is in the folder now: {{.Count}} packages.", len(files), args{"Version": strconv.FormatInt(r.Version, 10)}))
		})
		return nil
	})
}

// checkAtStart offers a newer package set at start-up when the user asked
// for that. A server that cannot be reached is asked again at the next
// start.
func (t *assistantTools) checkAtStart() {
	if !t.ap.u.app.Preferences().Bool(prefAssistantAutoDownload) {
		return
	}
	dir := t.ap.folder.Text
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		r, err := update.Latest(ctx, assistantHTTP, assistantServer)
		if err != nil {
			return
		}
		have, _, _ := vap.ReadIndex(dir)
		if have >= r.Version {
			return
		}
		fyne.Do(func() {
			if t.ap.folder.Text == dir {
				t.offer(dir, r, have, false, 0)
			}
		})
	}()
}

// managePrivate opens the private assistants: the packages in
// privateFolder, which can be added and deleted.
func (t *assistantTools) managePrivate() {
	u := t.ap.u
	dir := privateFolder(t.ap.folder.Text)
	rows := container.NewVBox()
	var refresh func()
	remove := func(path string) {
		confirm(u, tr("Manage private assistants"), tr("Delete {{.File}} from the private assistants?", args{"File": filepath.Base(path)}),
			tr("Delete"), func() {
				os.Chmod(path, 0o644) // VEConfigure may have made it read-only
				if err := os.Remove(path); err != nil {
					showError(u, err)
				}
				refresh()
			})
	}
	row := func(path, title, detail string) fyne.CanvasObject {
		name := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		name.Wrapping = fyne.TextWrapWord
		del := widget.NewButtonWithIcon(tr("Delete"), theme.DeleteIcon(), func() { remove(path) })
		return container.NewBorder(nil, nil, nil, container.NewCenter(del),
			container.NewVBox(name, captionText(detail, theme.ColorNamePlaceHolder)))
	}
	refresh = func() {
		rows.Objects = nil
		list, bad, err := vap.List(dir)
		switch {
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			rows.Add(mutedText(tr("This folder cannot be read: {{.Err}}", args{"Err": err})))
		case len(list)+len(bad) == 0:
			rows.Add(emptyState(theme.FolderIcon(), tr("No private assistants"), tr("Add an assistant package of your own to keep it here.")))
		}
		for _, in := range list {
			title := in.Name
			if p, err := vap.Load(in.Path); err == nil {
				title = packageTitle(p, in.Name)
			}
			rows.Add(row(in.Path, title, tr("{{.File}} · ID {{.ID}}", args{"File": filepath.Base(in.Path), "ID": assistantID(in.ID)})))
		}
		for path := range bad {
			rows.Add(row(path, filepath.Base(path), tr("Not an assistant package that can be read")))
		}
		rows.Refresh()
	}
	add := widget.NewButtonWithIcon(tr("Add private assistant…"), theme.ContentAddIcon(), func() {
		showFileOpen(u.win, storage.NewExtensionFileFilter([]string{".vap", ".dvap"}), func(r fyne.URIReadCloser, err error) {
			if err != nil {
				showError(u, err)
				return
			}
			if r == nil {
				return
			}
			name := r.URI().Name()
			b, err := io.ReadAll(r)
			r.Close()
			if err == nil {
				_, err = vap.Parse(b)
			}
			if err != nil {
				showError(u, &uiError{err: err, msg: tr("{{.File}} is not an assistant package that can be read.", args{"File": name})})
				return
			}
			path := filepath.Join(dir, name)
			write := func() {
				err := os.MkdirAll(dir, 0o755)
				if err == nil {
					os.Chmod(path, 0o644)
					err = os.WriteFile(path, b, 0o644)
				}
				if err != nil {
					showError(u, err)
				}
				refresh()
			}
			if _, err := os.Stat(path); err == nil {
				confirm(u, tr("Manage private assistants"), tr("{{.File}} is already a private assistant. Replace it?", args{"File": name}), tr("Replace"), write)
				return
			}
			write()
		})
	})
	note := mutedText(tr("Private assistants are your own assistant packages (.vap or .dvap), kept in {{.Folder}}. "+
		"{{.App}} does not list them under Add assistant yet.", args{"Folder": dir, "App": appName}))
	scroll := container.NewVScroll(rows)
	scroll.SetMinSize(fyne.NewSize(520, 260))
	refresh()
	d := dialog.NewCustom(tr("Manage private assistants"), tr("Close"),
		container.NewBorder(container.NewVBox(note, container.NewHBox(add)), nil, nil, nil, scroll), u.win)
	d.Show()
}

// errorReport shows the report on the assistants, to copy or save.
func (t *assistantTools) errorReport() {
	u := t.ap.u
	text := assistantReport(t.ap)
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapBreak
	l.TextStyle.Monospace = true
	scroll := container.NewVScroll(l)
	scroll.SetMinSize(fyne.NewSize(560, 300))
	copyText := widget.NewButtonWithIcon(tr("Copy"), theme.ContentCopyIcon(), func() { u.app.Clipboard().SetContent(text) })
	save := widget.NewButtonWithIcon(tr("Save…"), theme.DocumentSaveIcon(), func() {
		showFileSave(u.win, "", storage.NewExtensionFileFilter([]string{".txt"}), "assistant-report.txt", func(w fyne.URIWriteCloser, err error) {
			if err != nil {
				showError(u, err)
				return
			}
			if w == nil {
				return
			}
			_, err = io.WriteString(w, text)
			if cerr := w.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				showError(u, err)
			}
		})
	})
	d := dialog.NewCustom(tr("Error report"), tr("Close"), container.NewBorder(
		mutedText(tr("Nothing is sent anywhere: copy the report into your question, or save it.")),
		container.NewHBox(copyText, save), nil, nil, scroll), u.win)
	d.Show()
}

// assistantReport is the text of Error report. It is in English, for
// whoever helps with it, apart from the summaries of assistant areas.
func assistantReport(ap *assistantsPage) string {
	u := ap.u
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s, %s/%s\n\n", appName, buildVersion(), runtime.GOOS, runtime.GOARCH)
	dir := ap.folder.Text
	fmt.Fprintf(&b, "Package folder: %s\n", dir)
	switch s := ap.scan; {
	case s == nil:
		b.WriteString("  still being read\n")
	case s.err != nil:
		fmt.Fprintf(&b, "  cannot be read: %v\n", s.err)
	default:
		version, by, err := vap.ReadIndex(dir)
		fmt.Fprintf(&b, "  %d packages; files that cannot be read: %d\n", len(s.paths), s.bad)
		if err != nil {
			fmt.Fprintf(&b, "  %s: %v\n", vap.IndexFile, err)
		} else {
			fmt.Fprintf(&b, "  package set %d, downloaded by %d\n", version, by)
		}
	}
	private := privateFolder(dir)
	if list, bad, err := vap.List(private); err == nil {
		fmt.Fprintf(&b, "Private assistants: %d in %s, %d that cannot be read\n", len(list), private, len(bad))
	} else {
		fmt.Fprintf(&b, "Private assistants: none (%s)\n", private)
	}
	b.WriteString("\n")
	if sf := u.settings.file; sf != nil {
		fmt.Fprintf(&b, "Settings file: %s, unit %s, firmware %d\n", sf.name, vebus.UnitLabel(sf.unit), sf.unit.Firmware())
		fmt.Fprintf(&b, "  assistant area: %s\n", fileAreaSummary(sf.unit))
		for _, id := range ap.installed {
			fmt.Fprintf(&b, "  %s %s\n", assistantID(id), ap.title(id))
		}
	} else {
		b.WriteString("Settings file: none\n")
	}
	switch s := u.sess; {
	case s == nil:
		b.WriteString("Connection: none\n")
	case s.demo:
		b.WriteString("Connection: demo\n")
	default:
		fmt.Fprintf(&b, "Connection: %s, %d baud\n", s.portName, s.baud)
	}
	return b.String()
}
