package main

import (
	"fyne.io/fyne/v2"

	"github.com/bolchisb/victron-veconfigure-ported/pkg/vebus"
)

// pageVisual returns the visual shown with the settings of tab, or nil for
// none. A page shows it to the right of its form when wide enough, else
// above it (withVisual).
func pageVisual(u *ui, tab vebus.Tab) fyne.CanvasObject { return nil }
