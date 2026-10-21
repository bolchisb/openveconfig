package gridcode

import (
	"context"
	"errors"
	"testing"
)

// fakeWriter records the writes it is given and can be set to fail at a
// setting ID.
type fakeWriter struct {
	done   []SendWrite
	failAt uint16 // 0 for none (81, 128-191 never 0)
	err    error
}

func (w *fakeWriter) WriteSetting(_ context.Context, id, value uint16) error {
	if w.failAt != 0 && id == w.failAt {
		return w.err
	}
	w.done = append(w.done, SendWrite{ID: id, Value: value})
	return nil
}

func TestSendPlanOrder(t *testing.T) {
	// old generation 0x05 in both words; grid code 3, version 2.
	old := Pair{A: 0x0500, B: 0x0500}
	params := map[uint16]uint16{190: 0x000A, 129: 0x1234, 160: 0x00FF}
	plan, err := SendPlan(old, 3, 2, params)
	if err != nil {
		t.Fatalf("SendPlan: %v", err)
	}
	next, _ := Stamp(old, 3, 2)
	want := []SendWrite{
		{ID: 81, Value: 3},
		{ID: CheckerWordA, Value: next.A},
		{ID: 129, Value: 0x1234},
		{ID: 160, Value: 0x00FF},
		{ID: 190, Value: 0x000A},
		{ID: CheckerWordB, Value: next.B},
	}
	if len(plan) != len(want) {
		t.Fatalf("plan length %d, want %d: %+v", len(plan), len(want), plan)
	}
	for i := range want {
		if plan[i] != want[i] {
			t.Errorf("write %d = %+v, want %+v", i, plan[i], want[i])
		}
	}
	// Check word B must be last and A must be second (81 first).
	if plan[0].ID != 81 || plan[1].ID != CheckerWordA || plan[len(plan)-1].ID != CheckerWordB {
		t.Errorf("order wrong: first %d, second %d, last %d", plan[0].ID, plan[1].ID, plan[len(plan)-1].ID)
	}
}

func TestSendPlanNoParams(t *testing.T) {
	plan, err := SendPlan(Pair{}, 0, 0, nil)
	if err != nil {
		t.Fatalf("SendPlan: %v", err)
	}
	if len(plan) != 3 || plan[0].ID != 81 || plan[1].ID != CheckerWordA || plan[2].ID != CheckerWordB {
		t.Fatalf("plan = %+v, want just 81, 128, 191", plan)
	}
}

func TestSendPlanRejectsCheckWordInParams(t *testing.T) {
	for _, id := range []uint16{CheckerWordA, CheckerWordB, 127, 192} {
		if _, err := SendPlan(Pair{}, 2, 0, map[uint16]uint16{id: 1}); err == nil {
			t.Errorf("SendPlan with param %d: no error", id)
		}
	}
}

func TestSendPlanRejectsOversizeCode(t *testing.T) {
	if _, err := SendPlan(Pair{}, 0x1FF, 0, nil); err == nil {
		t.Error("SendPlan with code 0x1FF: no error")
	}
}

func TestSendRefusedAtFirstWrite(t *testing.T) {
	plan, _ := SendPlan(Pair{}, 2, 1, map[uint16]uint16{129: 7})
	// A device that refuses the very first write (the grid code, 81) — as a
	// real device does without the installer password for the grid words —
	// yields a *SendError with nothing written and the rest pending.
	w := &fakeWriter{failAt: 81, err: errors.New("access level required")}
	rep, err := Send(context.Background(), w, plan)
	if rep != nil {
		t.Errorf("rep = %+v, want nil", rep)
	}
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err is not *SendError: %v", err)
	}
	if se.At == nil || se.At.ID != 81 {
		t.Fatalf("At = %+v, want refusal at setting 81", se.At)
	}
	if len(se.Done) != 0 || len(se.Pending) != len(plan)-1 {
		t.Errorf("Done %d, Pending %d; want nothing written and the rest pending", len(se.Done), len(se.Pending))
	}
	if len(w.done) != 0 {
		t.Errorf("writer got %d writes; want none after the first is refused", len(w.done))
	}
}

func TestSendReportsPartialState(t *testing.T) {
	plan, _ := SendPlan(Pair{}, 2, 1, map[uint16]uint16{129: 7, 130: 8})
	// Refuse the second parameter word (130): 81, 128, 129 go out first.
	w := &fakeWriter{failAt: 130, err: errors.New("access level required")}
	rep, err := Send(context.Background(), w, plan)
	if rep != nil {
		t.Errorf("rep = %+v, want nil on failure", rep)
	}
	var se *SendError
	if !errors.As(err, &se) {
		t.Fatalf("err is not *SendError: %v", err)
	}
	if se.At == nil || se.At.ID != 130 {
		t.Fatalf("At = %+v, want setting 130", se.At)
	}
	gotDone := PlanIDs(se.Done)
	wantDone := []uint16{81, CheckerWordA, 129}
	if !equalIDs(gotDone, wantDone) {
		t.Errorf("Done = %v, want %v", gotDone, wantDone)
	}
	// 191 (and 130 itself) are still pending, so B was never written.
	if len(se.Pending) == 0 || se.Pending[len(se.Pending)-1].ID != CheckerWordB {
		t.Errorf("Pending = %v, want it to still include check word B last", PlanIDs(se.Pending))
	}
}

func TestSendContextCancelled(t *testing.T) {
	plan, _ := SendPlan(Pair{}, 2, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &fakeWriter{}
	_, err := Send(ctx, w, plan)
	var se *SendError
	if !errors.As(err, &se) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want cancelled *SendError", err)
	}
	if len(se.Done) != 0 || len(w.done) != 0 {
		t.Errorf("cancelled before any write, but Done %d / writer %d", len(se.Done), len(w.done))
	}
}

func equalIDs(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
