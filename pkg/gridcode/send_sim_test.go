package gridcode_test

// Acceptance tests for the grid send (#52, acceptance criterion 4): the full
// sequence and every refusal point are driven against the simulator.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bolchisb/openveconfig/pkg/gridcode"
	"github.com/bolchisb/openveconfig/pkg/mk2"
	"github.com/bolchisb/openveconfig/pkg/simulator"
)

// startGrid runs a simulator whose single device has the grid settings, with
// the given password-required IDs modelled, and returns an mk2.Client. When
// any ID is password-required an access password is configured so the device
// refuses it (Config.AccessPasswords); the test never grants access.
func startGrid(t *testing.T, passwordRequired ...uint16) *mk2.Client {
	t.Helper()
	cfg := simulator.Config{
		VersionInterval: -1,
		Devices: []simulator.Device{{
			Address: 0,
			Profile: simulator.Profile{
				Name:          "grid test",
				Version:       2699481,
				MinInputLimit: 20, MaxInputLimit: 500, InputLimit: 160,
				Settings: simulator.GridSettings(passwordRequired...),
			},
		}},
	}
	if len(passwordRequired) > 0 {
		cfg.AccessPasswords = []string{"never-granted"}
	}
	s := simulator.New(cfg)
	c := mk2.NewClient(s.Port(), &mk2.Options{Timeout: 100 * time.Millisecond, IgnoreMissingWriteAck: true})
	t.Cleanup(func() { c.Close() })
	if err := c.SetAddress(context.Background(), 0); err != nil {
		t.Fatalf("SetAddress: %v", err)
	}
	return c
}

func read(t *testing.T, c *mk2.Client, id uint16) uint16 {
	t.Helper()
	v, err := c.ReadSetting(context.Background(), id)
	if err != nil {
		t.Fatalf("ReadSetting(%d): %v", id, err)
	}
	return v
}

// TestSendSimulatorFullSequence runs the whole send against the simulator and
// checks every write landed, with the check words stamped for the next
// generation.
func TestSendSimulatorFullSequence(t *testing.T) {
	c := startGrid(t)
	// The device starts at generation 0 in both check words.
	old := gridcode.Pair{A: read(t, c, gridcode.CheckerWordA), B: read(t, c, gridcode.CheckerWordB)}
	params := map[uint16]uint16{129: 0x1234, 148: 0x00FF, 190: 0x0007}
	plan, err := gridcode.SendPlan(old, 3, 2, params)
	if err != nil {
		t.Fatalf("SendPlan: %v", err)
	}
	rep, err := gridcode.Send(context.Background(), c, true, plan)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(rep.Done) != len(plan) {
		t.Fatalf("Done %d writes, want %d", len(rep.Done), len(plan))
	}
	if got := read(t, c, 81); got != 3 {
		t.Errorf("grid code = %d, want 3", got)
	}
	for id, v := range params {
		if got := read(t, c, id); got != v {
			t.Errorf("setting %d = 0x%04X, want 0x%04X", id, got, v)
		}
	}
	next, _ := gridcode.Stamp(old, 3, 2)
	a, b := read(t, c, gridcode.CheckerWordA), read(t, c, gridcode.CheckerWordB)
	if a != next.A || b != next.B {
		t.Errorf("check words = A 0x%04X B 0x%04X, want A 0x%04X B 0x%04X", a, b, next.A, next.B)
	}
	// The stamped pair is consistent for the chosen code and version.
	if !(gridcode.Pair{A: a, B: b}).Consistent(3, 2) {
		t.Errorf("check words A 0x%04X B 0x%04X are not consistent for code 3 version 2", a, b)
	}
}

// TestSendSimulatorRefusalPoints drives a refusal at each step of the
// sequence (the grid code, check word A, a parameter word, check word B) and
// checks that Send reports which writes had already gone out and that the
// device holds exactly those.
func TestSendSimulatorRefusalPoints(t *testing.T) {
	params := map[uint16]uint16{129: 0x1111, 191 - 1: 0x2222}
	cases := []struct {
		name    string
		refuse  uint16
		wantGen []uint16 // IDs expected to have reached the device (Done)
	}{
		{"grid code 81", 81, nil},
		{"check word A 128", gridcode.CheckerWordA, []uint16{81}},
		{"parameter word 129", 129, []uint16{81, gridcode.CheckerWordA}},
		{"check word B 191", gridcode.CheckerWordB, []uint16{81, gridcode.CheckerWordA, 129, 190}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := startGrid(t, tc.refuse)
			old := gridcode.Pair{A: read(t, c, gridcode.CheckerWordA), B: read(t, c, gridcode.CheckerWordB)}
			plan, err := gridcode.SendPlan(old, 3, 2, params)
			if err != nil {
				t.Fatalf("SendPlan: %v", err)
			}
			rep, err := gridcode.Send(context.Background(), c, true, plan)
			if rep != nil {
				t.Errorf("rep = %+v, want nil on refusal", rep)
			}
			var se *gridcode.SendError
			if !errors.As(err, &se) {
				t.Fatalf("err is not *SendError: %v", err)
			}
			if !errors.Is(err, mk2.ErrAccessLevel) {
				t.Errorf("err = %v, want it to wrap mk2.ErrAccessLevel", err)
			}
			if se.At == nil || se.At.ID != tc.refuse {
				t.Fatalf("At = %+v, want refusal at %d", se.At, tc.refuse)
			}
			got := gridcode.PlanIDs(se.Done)
			if len(got) != len(tc.wantGen) {
				t.Fatalf("Done = %v, want %v", got, tc.wantGen)
			}
			for i := range tc.wantGen {
				if got[i] != tc.wantGen[i] {
					t.Fatalf("Done = %v, want %v", got, tc.wantGen)
				}
			}
			// The device holds the Done writes and nothing past the refusal.
			done := map[uint16]bool{}
			for _, w := range se.Done {
				if got := read(t, c, w.ID); got != w.Value {
					t.Errorf("setting %d = 0x%04X, want the written 0x%04X", w.ID, got, w.Value)
				}
				done[w.ID] = true
			}
			// Check word B is written last, so a refusal before it leaves B
			// at its old value: the device refuses the grid data as incomplete.
			if !done[gridcode.CheckerWordB] {
				if b := read(t, c, gridcode.CheckerWordB); b != old.B {
					t.Errorf("check word B = 0x%04X, want unchanged 0x%04X (B never written)", b, old.B)
				}
			}
		})
	}
}

// TestSendSimulatorGateDoesNotApply confirms the simulator path is allowed
// (the gate only refuses real devices).
func TestSendSimulatorGateDoesNotApply(t *testing.T) {
	if err := gridcode.CheckSendGate(true); err != nil {
		t.Errorf("CheckSendGate(simulator) = %v, want nil", err)
	}
	if err := gridcode.CheckSendGate(false); !errors.Is(err, gridcode.ErrNotHardwareValidated) {
		t.Errorf("CheckSendGate(real) = %v, want ErrNotHardwareValidated", err)
	}
}
