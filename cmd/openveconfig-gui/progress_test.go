package main

import (
	"context"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
)

// TestProgressKeepsDialogs: a dialog that the operation shows stays open
// when the progress dialog closes.
func TestProgressKeepsDialogs(t *testing.T) {
	u := newTestUI(t)
	runWithProgress(u, "", func(context.Context, func(done, total int)) error {
		fyne.Do(func() { showMessage(u, "", "shown by the operation") })
		return nil
	})
	waitFor(t, "only the operation's dialog", func() bool {
		o := u.win.Canvas().Overlays()
		return len(o.List()) == 1 && strings.Contains(dialogTexts(o.Top()), "shown by the operation")
	})
}
