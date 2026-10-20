package main

// Export settings and Print settings (File menu), as the original tool
// offers them: a readable list of the loaded settings, page by page. The
// list can be copied or saved as text. Fyne cannot print, so printing
// writes the list as an HTML page and opens it in the browser, which shows
// its print dialog.

import (
	"errors"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/gridcode"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// reportGroup is a group of settings in the report: its title and its
// rows of name and value.
type reportGroup struct {
	title string
	rows  [][2]string
}

// reportPage is a page of the report.
type reportPage struct {
	title  string
	groups []reportGroup
}

// settingsReport returns the settings of the loaded model as the pages
// show them, with the grid code and its parameters on the grid page.
func settingsReport(u *ui) []reportPage {
	d := u.settings.dev
	if d == nil {
		return nil
	}
	var pages []reportPage
	for _, p := range u.pages {
		if !slices.Contains(settingsTabSet, p.tab) && p.tab != vebus.TabGrid {
			continue
		}
		rp := reportPage{title: p.title}
		index := map[string]int{}
		for _, e := range d.Entries() {
			if e.Tab != p.tab || e.FlagWord {
				continue
			}
			g := entryGroup(e)
			i, ok := index[g]
			if !ok {
				i = len(rp.groups)
				index[g] = i
				rp.groups = append(rp.groups, reportGroup{title: groupLabel(g)})
			}
			value := entryValueText(d, e.ID())
			if e.ID() == vebus.SettingID(gridCodeSetting) {
				raw, _ := d.Raw(e.ID())
				value = gridCodeLabel(gridcode.Code(raw), u.settings.grid, d.Firmware)
			}
			rp.groups[i].rows = append(rp.groups[i].rows, [2]string{settingName(d, e.ID()), value})
		}
		if p.tab == vebus.TabGrid {
			rp.groups = append(rp.groups, gridReport(d, u.settings.grid)...)
		}
		if len(rp.groups) > 0 {
			pages = append(pages, rp)
		}
	}
	return pages
}

// gridReport returns the grid parameters of d's grid code, by group.
func gridReport(d *vebus.Device, grid gridcode.Model) []reportGroup {
	code, err := d.Raw(vebus.SettingID(gridCodeSetting))
	if err != nil {
		return nil
	}
	p, _ := gridPageFor(int(code), grid)
	if p == nil {
		return nil
	}
	var groups []reportGroup
	index := map[string]int{}
	for _, prm := range p.Params {
		word, err := d.Raw(vebus.SettingID(prm.Word))
		if err != nil {
			continue
		}
		if show, _ := d.Raw(vebus.SettingID(prm.ShowWord)); !prm.Shown(show) {
			continue
		}
		var high uint16
		if prm.High != 0 {
			if high, err = d.Raw(vebus.SettingID(prm.High)); err != nil {
				continue
			}
		}
		f := prm.FieldWords(word, high)
		i, ok := index[prm.Group]
		if !ok {
			i = len(groups)
			index[prm.Group] = i
			title := gridText(p, prm.Group)
			if prm.Group == "" {
				title = gridText(p, p.Name)
				if p.Title != "" {
					title = gridText(p, p.Title)
				}
			}
			groups = append(groups, reportGroup{title: title})
		}
		var value string
		switch prm.Kind {
		case gridcode.Checkbox:
			value = tr("off")
			if prm.Value(f) == 1 {
				value = tr("on")
			}
		case gridcode.Choice:
			value = gridText(p, prm.Choices[f])
			if value == "" {
				value = tr("Value {{.Value}}", args{"Value": strconv.Itoa(f)})
			}
		default:
			value = paramText(prm, prm.Value(f))
		}
		groups[i].rows = append(groups[i].rows, [2]string{gridText(p, prm.Name), value})
	}
	return groups
}

// reportSource names where the loaded settings come from.
func reportSource(u *ui) string {
	m := u.settings
	fw := strconv.FormatUint(uint64(m.dev.Firmware), 10)
	if m.file != nil {
		return tr("Settings file {{.File}}, firmware {{.Firmware}}", args{"File": m.file.name, "Firmware": fw})
	}
	return tr("Device at address {{.Address}}, firmware {{.Firmware}}", args{"Address": m.dev.Address, "Firmware": fw})
}

// reportText returns the report as plain text.
func reportText(u *ui) string {
	var b strings.Builder
	b.WriteString(appName + " – " + reportSource(u) + "\n")
	for _, p := range settingsReport(u) {
		b.WriteString("\n" + p.title + "\n" + strings.Repeat("=", len([]rune(p.title))) + "\n")
		for _, g := range p.groups {
			b.WriteString("\n  " + g.title + "\n")
			for _, r := range g.rows {
				b.WriteString("    " + r[0] + ": " + r[1] + "\n")
			}
		}
	}
	return b.String()
}

// reportHTML returns the report as an HTML page that opens the print
// dialog when shown.
func reportHTML(u *ui) string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html><head><meta charset="utf-8"><title>` + e(appName) + `</title><style>
body{font-family:sans-serif;font-size:10pt;margin:1.5cm}h1{font-size:14pt}h2{font-size:12pt;margin-top:1.2em;page-break-after:avoid}
h3{font-size:10pt;margin:.8em 0 .2em}table{border-collapse:collapse;width:100%}td{border-bottom:1px solid #ccc;padding:2px 4px}
td:first-child{width:60%}</style></head><body onload="window.print()">`)
	b.WriteString("<h1>" + e(appName) + "</h1><p>" + e(reportSource(u)) + "</p>")
	for _, p := range settingsReport(u) {
		b.WriteString("<h2>" + e(p.title) + "</h2>")
		for _, g := range p.groups {
			b.WriteString("<h3>" + e(g.title) + "</h3><table>")
			for _, r := range g.rows {
				b.WriteString("<tr><td>" + e(r[0]) + "</td><td>" + e(r[1]) + "</td></tr>")
			}
			b.WriteString("</table>")
		}
	}
	b.WriteString("</body></html>")
	return b.String()
}

// exportSettings shows the report, to be copied or saved as a text file.
func exportSettings(u *ui) {
	if u.settings.dev == nil {
		return
	}
	text := reportText(u)
	list := widget.NewLabel(text)
	list.TextStyle.Monospace = true
	copyText := widget.NewButtonWithIcon(tr("Copy"), theme.ContentCopyIcon(), func() {
		u.app.Clipboard().SetContent(text)
	})
	save := widget.NewButtonWithIcon(tr("Save…"), theme.DocumentSaveIcon(), func() {
		dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
			if err == nil && w != nil {
				_, err = w.Write([]byte(text))
				err = errors.Join(err, w.Close())
			}
			if err != nil {
				showError(u, err)
			}
		}, u.win)
		dlg.SetTitleText(tr("Select file to save exported settings"))
		dlg.SetFilter(storage.NewExtensionFileFilter([]string{".txt"}))
		dlg.SetFileName("settings.txt")
		dlg.Show()
	})
	content := container.NewBorder(nil, container.NewHBox(copyText, save), nil, nil, container.NewScroll(list))
	showTableDialog(u, tr("Export"), content, tableDialogSize, tr("Close"), nil, nil)
}

// printSettings writes the report as an HTML page and opens it in the
// browser, which prints it.
func printSettings(u *ui) {
	if u.settings.dev == nil {
		return
	}
	f, err := os.CreateTemp("", "openveconfig-settings-*.html")
	if err == nil {
		_, err = f.WriteString(reportHTML(u))
		err = errors.Join(err, f.Close())
	}
	if err == nil {
		path := filepath.ToSlash(f.Name())
		if !strings.HasPrefix(path, "/") {
			path = "/" + path // a Windows drive letter
		}
		err = u.app.OpenURL(&url.URL{Scheme: "file", Path: path})
	}
	if err != nil {
		showError(u, err)
	}
}
