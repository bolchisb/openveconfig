package main

// The firmware-update wizard (#38, epic #46): pick a .vff file, confirm the
// device, confirm the warning that flashing resets the settings and can brick
// the device, back the settings up to a .rvsc file, then flash with progress.
//
// SAFETY (#38): writing firmware to a real serial port is not validated on
// hardware yet, so the wizard runs fully only against the demo simulator. On
// a real connection it stops at the confirm step with fw.CheckHardwareGate's
// refusal and offers no way to override it; opening that path is a hardware
// task, not a GUI toggle. The reprogram transport for the demo is an
// in-process fake bootloader (fw.BootSim), as in "openveconfig-cli firmware
// flash --simulator".

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	fw "github.com/bolchisb/openveconfig/pkg/firmware"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vff"
)

// The wizard steps, in order.
const (
	fwStepFile = iota
	fwStepTarget
	fwStepConfirm
	fwStepProgress
	fwStepResult
)

// firmwareWizard is the firmware-update dialog of one connection.
type firmwareWizard struct {
	u                  *ui
	dlg                dialog.Dialog
	body               *fyne.Container
	back, next, cancel *widget.Button

	step int

	// The chosen firmware file.
	filename string
	file     *vff.File
	fileErr  error

	// Flashing.
	bar      *widget.ProgressBar
	status   *widget.Label
	result   error // the outcome shown at fwStepResult; nil means success
	cancelFn context.CancelFunc
}

// showFirmwareWizard opens the wizard. It needs an open connection.
func showFirmwareWizard(u *ui) {
	if u.sess == nil {
		showMessage(u, tr("Firmware update"),
			tr("Connect to a device first (Target ▸ Com port), then start the firmware update."))
		return
	}
	(&firmwareWizard{u: u}).show()
}

func (w *firmwareWizard) show() {
	w.body = container.NewStack()
	w.cancel = widget.NewButtonWithIcon(tr("Cancel"), theme.CancelIcon(), w.close)
	w.back = widget.NewButtonWithIcon(tr("Back"), theme.NavigateBackIcon(), w.goBack)
	w.next = widget.NewButtonWithIcon(tr("Next"), theme.NavigateNextIcon(), w.goNext)
	w.next.Importance = widget.HighImportance
	w.next.IconPlacement = widget.ButtonIconTrailingText
	scroll := container.NewVScroll(w.body)
	scroll.SetMinSize(fyne.NewSize(560, 360))
	d := dialog.NewCustomWithoutButtons(tr("Firmware update"), scroll, w.u.win)
	d.SetButtons([]fyne.CanvasObject{w.cancel, w.back, w.next})
	w.dlg = d
	w.render()
	w.dlg.Show()
}

// close cancels any running flash and hides the dialog.
func (w *firmwareWizard) close() {
	if w.cancelFn != nil {
		w.cancelFn()
	}
	w.dlg.Hide()
}

func (w *firmwareWizard) goNext() {
	switch w.step {
	case fwStepFile:
		if w.file == nil {
			return
		}
		w.step = fwStepTarget
		w.render()
	case fwStepTarget:
		w.step = fwStepConfirm
		w.render()
	case fwStepConfirm:
		if !w.u.sess.demo {
			return // real hardware is gated off; Next is disabled anyway
		}
		w.startFlash()
	case fwStepResult:
		w.close()
	}
}

func (w *firmwareWizard) goBack() {
	if w.step == fwStepTarget || w.step == fwStepConfirm {
		w.step--
		w.render()
	}
}

// render shows the current step and sets the buttons for it.
func (w *firmwareWizard) render() {
	w.back.Enable()
	w.next.Enable()
	w.cancel.Show()
	w.next.SetText(tr("Next"))
	w.next.SetIcon(theme.NavigateNextIcon())

	var view fyne.CanvasObject
	switch w.step {
	case fwStepFile:
		w.back.Disable()
		if w.file == nil {
			w.next.Disable()
		}
		view = w.fileView()
	case fwStepTarget:
		view = w.targetView()
	case fwStepConfirm:
		view = w.confirmView()
		if w.u.sess.demo {
			w.next.SetText(tr("Back up and flash"))
			w.next.SetIcon(theme.MediaPlayIcon())
		} else {
			w.next.Disable()
		}
	case fwStepProgress:
		w.back.Disable()
		w.next.Disable()
		view = w.progressView()
	case fwStepResult:
		w.back.Disable()
		w.cancel.Hide()
		w.next.SetText(tr("Close"))
		w.next.SetIcon(theme.ConfirmIcon())
		view = w.resultView()
	}
	w.body.Objects = []fyne.CanvasObject{view}
	w.body.Refresh()
}

// fileView is step 1: choose the .vff file.
func (w *firmwareWizard) fileView() fyne.CanvasObject {
	choose := widget.NewButtonWithIcon(tr("Choose file…"), theme.FolderOpenIcon(), func() {
		showFileOpen(w.u.win, storage.NewExtensionFileFilter([]string{".vff"}), func(r fyne.URIReadCloser, err error) {
			if err != nil {
				showError(w.u, err)
				return
			}
			if r != nil {
				w.loadFile(r)
			}
		})
	})
	box := container.NewVBox(
		boldLabel(tr("Step 1 · Choose the firmware file")),
		wrapLabel(tr("Choose the .vff firmware file to flash to the device.")),
		container.NewHBox(choose))
	if w.filename != "" {
		box.Add(widget.NewLabel(tr("Selected: {{.File}}", args{"File": w.filename})))
	}
	switch {
	case w.fileErr != nil:
		box.Add(callout(theme.NewErrorThemedResource(theme.ErrorIcon()),
			tr("This file cannot be read as firmware: {{.Err}}", args{"Err": w.fileErr})))
	case w.file != nil:
		box.Add(callout(theme.InfoIcon(), w.fileSummary()))
	}
	return box
}

// loadFile reads and parses the chosen firmware file.
func (w *firmwareWizard) loadFile(r fyne.URIReadCloser) {
	defer r.Close()
	w.filename = r.URI().Name()
	data, err := io.ReadAll(r)
	if err == nil {
		w.file, err = vff.Parse(data)
	}
	if err != nil {
		w.file, w.fileErr = nil, err
	} else {
		w.fileErr = nil
	}
	w.render()
}

// fileSummary describes the parsed file.
func (w *firmwareWizard) fileSummary() string {
	ver := tr("none")
	if v, ok := w.file.Version(); ok {
		ver = fmt.Sprint(v)
	}
	return tr("Firmware version {{.Version}}, {{.Parts}} part(s), all checks passed.",
		args{"Version": ver, "Parts": strconv.Itoa(len(w.file.Parts))})
}

// targetView is step 2: confirm the connection and the target device.
func (w *firmwareWizard) targetView() fyne.CanvasObject {
	s := w.u.sess
	where := s.portName
	if s.demo {
		where = tr("the demo simulator")
	}
	box := container.NewVBox(
		boldLabel(tr("Step 2 · Confirm the device")),
		widget.NewLabel(tr("Connected to {{.Port}}.", args{"Port": where})),
		widget.NewLabel(tr("The firmware will be flashed to the device at address {{.Address}}, firmware {{.Firmware}}.",
			args{"Address": strconv.Itoa(int(s.target)), "Firmware": s.firmwareText(s.target)})))
	if w.file != nil {
		box.Add(mutedText(w.fileSummary()))
	}
	return box
}

// confirmView is step 3: the warning and, on real hardware, the gate.
func (w *firmwareWizard) confirmView() fyne.CanvasObject {
	box := container.NewVBox(
		boldLabel(tr("Step 3 · Confirm")),
		callout(theme.WarningIcon(), tr("Flashing replaces the firmware and RESETS ALL SETTINGS to the factory defaults. "+
			"A wrong file or an interrupted update can leave the device unusable (bricked).")),
		wrapLabel(tr("Your settings are saved to a .rvsc backup file before anything is flashed. "+
			"You can apply the backup again afterwards with the rvsc-apply command.")))
	if !w.u.sess.demo {
		box.Add(callout(theme.NewErrorThemedResource(theme.ErrorIcon()),
			tr("Flashing a real device is not available yet: it is not validated on hardware (#38). "+
				"This wizard runs fully only against the demo. Nothing will be written to this device.")))
	}
	return box
}

// progressView is step 4: the progress bar and status.
func (w *firmwareWizard) progressView() fyne.CanvasObject {
	if w.bar == nil {
		w.bar = widget.NewProgressBar()
	}
	if w.status == nil {
		w.status = widget.NewLabel("")
		w.status.Wrapping = fyne.TextWrapWord
	}
	return container.NewVBox(boldLabel(tr("Step 4 · Flashing")), w.status, w.bar)
}

// resultView is step 5: success or the error.
func (w *firmwareWizard) resultView() fyne.CanvasObject {
	if w.result == nil {
		return container.NewVBox(
			boldLabel(tr("Done")),
			callout(theme.ConfirmIcon(), tr("The firmware was flashed. The device has been reset to its default settings; "+
				"apply your backup with rvsc-apply to restore them.")))
	}
	return container.NewVBox(
		boldLabel(tr("The update did not finish")),
		callout(theme.NewErrorThemedResource(theme.ErrorIcon()), w.result.Error()))
}

// startFlash asks where to save the backup, then runs the backup and flash.
// It never reaches a real device: the gate refusal is shown as the result.
func (w *firmwareWizard) startFlash() {
	if err := fw.CheckHardwareGate(w.u.sess.demo, false); err != nil {
		w.result = err
		w.step = fwStepResult
		w.render()
		return
	}
	showFileSave(w.u.win, tr("Save a backup of the device settings"),
		storage.NewExtensionFileFilter([]string{".rvsc"}), backupName(w.filename),
		func(wc fyne.URIWriteCloser, err error) {
			if err != nil {
				showError(w.u, err)
				return
			}
			if wc == nil {
				return // cancelled: do not flash without a backup
			}
			w.step = fwStepProgress
			w.render()
			w.run(wc)
		})
}

// run flashes in the background and shows the result when it returns.
func (w *firmwareWizard) run(wc fyne.URIWriteCloser) {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelFn = cancel
	sess := w.u.sess
	file := w.file
	target := fw.Target{FirmwareNumber: sess.firmware[sess.target], BootblockVersion: -1, BootblockRevision: -1}
	go func() {
		err := w.flash(ctx, sess, wc, file, target)
		fyne.Do(func() {
			cancel()
			w.cancelFn = nil
			w.result = err
			w.step = fwStepResult
			w.render()
		})
	}()
}

// flash backs the settings up to wc, pre-flights the file and flashes it.
func (w *firmwareWizard) flash(ctx context.Context, sess *session, wc fyne.URIWriteCloser, file *vff.File, target fw.Target) error {
	w.setStatus(tr("Backing up the device settings…"))
	var data []byte
	err := sess.Do(ctx, func(ctx context.Context, c *mk2.Client) error {
		bf, err := fw.BackupSettings(ctx, c, sess.target, nil)
		if err != nil {
			return err
		}
		data, err = bf.MarshalBinary()
		return err
	})
	if err == nil {
		_, err = wc.Write(data)
	}
	if cerr := wc.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%s: %w", tr("the settings backup failed"), err)
	}

	if err := fw.Preflight(file, target); err != nil {
		return err
	}

	// The demo flashes an in-process fake bootloader; real hardware never
	// reaches here (the gate stops it at the confirm step).
	t := fw.NewBootSim(fw.BootConfig{})
	defer t.Close()
	rp := fw.NewReprogrammer(t, &fw.Options{Progress: w.onProgress})
	defer rp.Close()
	if err := rp.EnterBootloader(ctx); err != nil {
		return err
	}
	return rp.Program(ctx, file, target)
}

// onProgress shows the reprogrammer's progress.
func (w *firmwareWizard) onProgress(p fw.Progress) {
	fyne.Do(func() {
		switch p.Phase {
		case fw.PhaseEntering:
			w.status.SetText(tr("Entering the bootloader…"))
			w.bar.SetValue(0)
		case fw.PhaseErasing:
			w.status.SetText(tr("Erasing the firmware…"))
			w.bar.SetValue(0)
		case fw.PhaseProgramming:
			w.status.SetText(tr("Programming block {{.Block}} of {{.Total}}…",
				args{"Block": strconv.Itoa(p.Block), "Total": strconv.Itoa(p.Total)}))
			if p.Total > 0 {
				w.bar.Max = float64(p.Total)
				w.bar.SetValue(float64(p.Block))
			}
		case fw.PhaseDone:
			w.status.SetText(tr("Finishing…"))
			if p.Total > 0 {
				w.bar.SetValue(float64(p.Total))
			}
		}
	})
}

// setStatus updates the status label from any goroutine.
func (w *firmwareWizard) setStatus(text string) {
	fyne.Do(func() { w.status.SetText(text) })
}

// backupName is the default name of the settings backup, from the firmware
// file name and the time.
func backupName(firmwareFile string) string {
	base := strings.TrimSuffix(firmwareFile, filepath.Ext(firmwareFile))
	if base == "" {
		base = "device"
	}
	return fmt.Sprintf("%s-backup-%s.rvsc", base, time.Now().Format("20060102-150405"))
}

// boldLabel is a bold heading label.
func boldLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.TextStyle.Bold = true
	return l
}

// wrapLabel is a word-wrapped label.
func wrapLabel(text string) *widget.Label {
	l := widget.NewLabel(text)
	l.Wrapping = fyne.TextWrapWord
	return l
}
