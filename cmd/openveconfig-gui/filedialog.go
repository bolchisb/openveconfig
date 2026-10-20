package main

import (
	"os"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver"
	"fyne.io/fyne/v2/storage"
	"github.com/ncruces/zenity"
)

// The file dialogs are the system's own (zenity). Fyne's dialogs are kept
// where there is none: a window without a native handle (tests), or Linux
// without zenity or kdialog.

// nativeOptions are the zenity options common to all file dialogs, or
// ok=false when w cannot show a system dialog.
func nativeOptions(w fyne.Window) (opts []zenity.Option, ok bool) {
	nw, isNative := w.(driver.NativeWindow)
	if !isNative || !zenity.IsAvailable() {
		return nil, false
	}
	opts = []zenity.Option{zenity.Modal()}
	nw.RunNative(func(ctx any) {
		switch c := ctx.(type) {
		case driver.WindowsWindowContext:
			if c.HWND != 0 {
				opts = append(opts, zenity.Attach(c.HWND))
			}
		case driver.X11WindowContext:
			if c.WindowHandle != 0 {
				opts = append(opts, zenity.Attach(int(c.WindowHandle)))
			}
		}
	})
	if runtime.GOOS == "darwin" {
		opts = append(opts, zenity.Attach(os.Getpid()))
	}
	return opts, true
}

// filterOption limits a system dialog to the extensions of f, an
// extension filter.
func filterOption(f storage.FileFilter) zenity.Option {
	ef, _ := f.(*storage.ExtensionFileFilter)
	if ef == nil {
		return zenity.FileFilters{}
	}
	pats := make([]string, len(ef.Extensions))
	for i, e := range ef.Extensions {
		pats[i] = "*" + e
	}
	return zenity.FileFilter{Patterns: pats, CaseFold: true}
}

// showFileOpen asks for a file to read, as dialog.NewFileOpen does: cb gets
// nil, nil when the dialog is cancelled.
func showFileOpen(w fyne.Window, filter storage.FileFilter, cb func(fyne.URIReadCloser, error)) {
	opts, ok := nativeOptions(w)
	if !ok {
		d := dialog.NewFileOpen(cb, w)
		d.SetFilter(filter)
		d.Show()
		return
	}
	opts = append(opts, filterOption(filter))
	go func() {
		path, err := zenity.SelectFile(opts...)
		var r fyne.URIReadCloser
		if err == nil {
			r, err = storage.Reader(storage.NewFileURI(path))
		}
		if err == zenity.ErrCanceled {
			err = nil
		}
		fyne.Do(func() { cb(r, err) })
	}()
}

// showFileSave asks for a file to write, as dialog.NewFileSave does; title
// may be empty for the system's own.
func showFileSave(w fyne.Window, title string, filter storage.FileFilter, name string, cb func(fyne.URIWriteCloser, error)) {
	opts, ok := nativeOptions(w)
	if !ok {
		d := dialog.NewFileSave(cb, w)
		if title != "" {
			d.SetTitleText(title)
		}
		d.SetFilter(filter)
		d.SetFileName(name)
		d.Show()
		return
	}
	opts = append(opts, filterOption(filter), zenity.Filename(name), zenity.ConfirmOverwrite())
	if title != "" {
		opts = append(opts, zenity.Title(title))
	}
	go func() {
		path, err := zenity.SelectFileSave(opts...)
		var wc fyne.URIWriteCloser
		if err == nil {
			wc, err = storage.Writer(storage.NewFileURI(path))
		}
		if err == zenity.ErrCanceled {
			err = nil
		}
		fyne.Do(func() { cb(wc, err) })
	}()
}

// showFolderOpen asks for a folder, starting in dir when it exists, as
// dialog.NewFolderOpen does.
func showFolderOpen(w fyne.Window, dir string, cb func(fyne.ListableURI, error)) {
	exists := false
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		exists = true
	}
	opts, ok := nativeOptions(w)
	if !ok {
		d := dialog.NewFolderOpen(cb, w)
		if exists {
			if l, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
				d.SetLocation(l)
			}
		}
		d.Show()
		return
	}
	opts = append(opts, zenity.Directory())
	if exists {
		opts = append(opts, zenity.Filename(dir))
	}
	go func() {
		path, err := zenity.SelectFile(opts...)
		var l fyne.ListableURI
		if err == nil {
			l, err = storage.ListerForURI(storage.NewFileURI(path))
		}
		if err == zenity.ErrCanceled {
			err = nil
		}
		fyne.Do(func() { cb(l, err) })
	}()
}
