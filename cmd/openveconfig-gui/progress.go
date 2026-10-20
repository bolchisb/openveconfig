package main

import (
	"context"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// runWithProgress runs op on a new goroutine and shows a modal dialog with
// title, a progress bar and a Cancel button that cancels op's context. op
// reports progress as done out of total steps; it may call progress from
// any goroutine. When op returns, the dialog closes and an error is shown,
// unless the user cancelled. op must use fyne.Do for its own UI updates.
// runWithProgress must be called on the UI goroutine.
func runWithProgress(u *ui, title string, op func(ctx context.Context, progress func(done, total int)) error) {
	ctx, cancel := context.WithCancel(context.Background())
	bar := widget.NewProgressBar()
	d := dialog.NewCustomWithoutButtons(title, bar, u.win)
	var stop *widget.Button
	stop = widget.NewButton(tr("Cancel"), func() {
		stop.Disable()
		cancel()
	})
	d.SetButtons([]fyne.CanvasObject{stop})
	overlays := u.win.Canvas().Overlays()
	below := len(overlays.List())
	d.Show()

	progress := func(done, total int) {
		fyne.Do(func() {
			if total > 0 {
				bar.Max = float64(total)
				bar.SetValue(float64(done))
			}
		})
	}
	go func() {
		err := op(ctx, progress)
		cancelled := ctx.Err() != nil
		cancel()
		fyne.Do(func() {
			// The overlay stack removes the overlays above d with it, such
			// as a dialog that op showed: put them back.
			var above []fyne.CanvasObject
			if l := overlays.List(); len(l) > below+1 {
				above = slices.Clone(l[below+1:])
			}
			d.Hide()
			for _, o := range above {
				overlays.Add(o)
			}
			if err != nil && !cancelled {
				showError(u, err)
			}
		})
	}()
}

// showError shows err in a dialog over the main window.
func showError(u *ui, err error) { showMessage(u, tr("Error"), err.Error()) }

// showMessage shows a message dialog over the main window.
func showMessage(u *ui, title, msg string) {
	d := dialog.NewInformation(title, msg, u.win)
	d.SetDismissText(tr("OK"))
	d.Show()
}
