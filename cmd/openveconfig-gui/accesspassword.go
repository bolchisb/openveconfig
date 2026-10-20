package main

// Access passwords. A device writes a setting whose access byte asks for a
// password (and most grid codes) only after it has accepted an access
// password, which the program hands it before writing
// (mk2.Client.SendAccessPassword, reverse-engineered, #9). The original
// tool also checks the password itself when it is entered; this program
// does not (docs/CLEAN-ROOM.md, Access control): it never checks, derives,
// keeps or saves a password. The passwords live in the session and are
// gone when it closes. That the device itself refuses a wrong password is
// inferred from the original's replies and not yet checked on hardware
// (#3, #52).

import (
	"context"
	"errors"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// accessPasswordsOnHardware enables handing access passwords to a real
// device. It stays off until the device's replies have been checked on
// hardware (#3, #52); the demo uses passwords already.
const accessPasswordsOnHardware = false

// demoPassword is the made-up password the demo's simulated devices accept.
const demoPassword = "DEMO"

// passwordsEnabled reports whether access passwords are handed to the
// devices of s.
func passwordsEnabled(s *session) bool { return s != nil && (s.demo || accessPasswordsOnHardware) }

// askAccessPasswords asks for the access passwords of s, as the original
// tool does, and calls done with whether the user gave some.
func askAccessPasswords(u *ui, s *session, done func(ok bool)) {
	entry := widget.NewPasswordEntry()
	prompt := widget.NewLabel(tr("Enter access password(s) (comma separated)."))
	note := widget.NewLabel(tr("This program does not check the password: it hands it to the device when the settings are sent, " +
		"and the device decides whether to accept the changes. The password is not kept or saved; it is forgotten when you disconnect."))
	note.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(prompt, entry, note)
	if s.demo {
		content.Add(widget.NewLabel(tr("The demo devices accept the password {{.Password}}.", args{"Password": demoPassword})))
	}
	dlg := dialog.NewCustomConfirm(tr("Access password"), tr("OK"), tr("Cancel"), content, func(ok bool) {
		pw := splitPasswords(entry.Text)
		if ok && len(pw) > 0 && u.sess == s {
			s.passwords = pw
			done(true)
			return
		}
		done(false)
	}, u.win)
	entry.OnSubmitted = func(string) { dlg.Hide(); dlg.Confirm() }
	dlg.Resize(fyne.NewSize(460, 0))
	dlg.Show()
	u.win.Canvas().Focus(entry)
}

// splitPasswords returns the comma-separated passwords of text.
func splitPasswords(text string) []string {
	var pw []string
	for _, p := range strings.Split(text, ",") {
		if p = strings.TrimSpace(p); p != "" {
			pw = append(pw, p)
		}
	}
	return pw
}

// changesNeedPassword reports whether a send of d would write a change that the
// device accepts only after an access password.
func changesNeedPassword(d *vebus.Device) bool { return countChanges(d).password > 0 }

// errPasswordRefused is shown when the device does not accept a password.
func errPasswordRefused(err error) error {
	return &uiError{err: err, msg: tr("The device did not accept the access password. Nothing was written.")}
}

// sendAccessPasswords hands passwords to the device at addr, before its
// settings are written. The first refused one stops it.
func sendAccessPasswords(ctx context.Context, c *mk2.Client, addr byte, passwords []string) error {
	if len(passwords) == 0 {
		return nil
	}
	if err := c.SetAddress(ctx, addr); err != nil {
		return err
	}
	for _, pw := range passwords {
		if err := c.SendAccessPassword(ctx, pw); err != nil {
			if errors.Is(err, mk2.ErrPasswordRefused) {
				return errPasswordRefused(err)
			}
			return err
		}
	}
	return nil
}
