package main

import (
	"errors"
	"slices"
	"testing"

	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/vebus"
)

// TestAccessPasswordWrite sends a setting the demo device protects with an
// access level: refused without a password, nothing written with a wrong
// one, written after the demo's password.
func TestAccessPasswordWrite(t *testing.T) {
	newTestUI(t) // an app for fyne.Do
	s, err := connectDemo(t)
	if err != nil {
		t.Fatal(err)
	}
	d := readModel(t, s, 0)
	id := vebus.SettingID(13) // access level 2 on the demo device
	if err := d.SetRaw(id, 1); err != nil {
		t.Fatal(err)
	}
	result := func(rep *vebus.WriteReport) vebus.WriteResult {
		t.Helper()
		i := slices.IndexFunc(rep.Results, func(r vebus.WriteResult) bool { return r.ID == id })
		if i < 0 {
			t.Fatalf("setting 13 not in the report")
		}
		return rep.Results[i]
	}

	rep, _ := writeSettings(ctxT(t), s, d, false, nil)
	if r := result(rep); r.Status != vebus.StatusFailed || !errors.Is(r.Err, mk2.ErrAccessLevel) {
		t.Fatalf("without a password: %+v", r)
	}

	s.passwords = []string{"WRONG"}
	if rep, err := writeSettings(ctxT(t), s, d, false, nil); rep != nil || !errors.Is(err, mk2.ErrPasswordRefused) {
		t.Fatalf("wrong password: %v, %v", rep, err)
	}
	if len(s.passwords) != 0 {
		t.Error("a refused password is kept")
	}

	s.passwords = []string{demoPassword}
	rep, err = writeSettings(ctxT(t), s, d, false, nil)
	if r := result(rep); err != nil || r.Status != vebus.StatusVerified {
		t.Fatalf("with the demo password: %+v, %v", r, err)
	}
}

func TestSplitPasswords(t *testing.T) {
	if got := splitPasswords(" A1, ,B2 ,"); !slices.Equal(got, []string{"A1", "B2"}) {
		t.Errorf("splitPasswords = %q", got)
	}
}
