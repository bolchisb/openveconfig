package wizard

import (
	"errors"
	"fmt"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

// Session walks a user through the steps of a program. A user interface
// calls Start, shows Current, and calls Next with the entered values or
// Back; when Current is nil the wizard is done and Answers holds every
// step's values for the build.
type Session struct {
	p *Program
	// Edit, when set, makes only the steps of that source wait for the
	// user; the others take their defaults (Session.Defaults).
	Edit string
	// Defaults are offered at the steps, for example the values read back
	// from a unit (Program.ReadBack).
	Defaults Answers

	order []Key   // the steps answered, in order
	ans   Answers // their values
	kept  Answers // answers undone by Back, offered again
	cur   *Run
}

// NewSession returns a session over p.
func (p *Program) NewSession(defaults Answers) *Session {
	return &Session{p: p, Defaults: defaults, ans: Answers{}}
}

// Start assembles the program up to the first step that waits and returns
// it (nil when no step waits).
func (s *Session) Start() (*Step, error) {
	run, err := s.p.Assemble(Options{State: UserEntry, Answers: s.ans, Defaults: s.offered(), Edit: s.Edit})
	if err != nil {
		return nil, err
	}
	s.cur = run
	return run.Pending, nil
}

// offered returns the defaults, with the session's own earlier answers
// (kept after Back) in front.
func (s *Session) offered() Answers {
	d := Answers{}
	for k, v := range s.Defaults {
		d[k] = v
	}
	for k, v := range s.kept {
		d[k] = v
	}
	return d
}

// Current returns the step that waits, or nil when the wizard is done.
func (s *Session) Current() *Step {
	if s.cur == nil {
		return nil
	}
	return s.cur.Pending
}

// Next answers the current step with values (one per label of the step;
// none for an information step) and returns the next step. An error step
// cannot be answered: go Back.
func (s *Session) Next(values []asm.Value) (*Step, error) {
	st := s.Current()
	if st == nil {
		return nil, errors.New("wizard: no step waits")
	}
	if st.Directive == ".AER" {
		return st, errors.New("wizard: an error step can only go back")
	}
	if err := check(st, values); err != nil {
		return st, err
	}
	s.order = append(s.order, st.Key)
	s.ans[st.Key] = append([]asm.Value(nil), values...)
	next, err := s.Start()
	if err != nil {
		s.Back()
		return st, err
	}
	return next, nil
}

// check validates values against a step.
func check(st *Step, values []asm.Value) error {
	if st.Directive == ".AIP" {
		return nil
	}
	if len(values) != len(st.Labels) {
		return fmt.Errorf("wizard: step %s takes %d values, got %d", st.Name, len(st.Labels), len(values))
	}
	for i, v := range values {
		n, err := v.AsInt()
		if err != nil {
			continue // not a number: lists and strings pass unchecked
		}
		fi := i
		if st.Type != 'E' {
			if i == 0 {
				if n < 0 || n >= int64(st.Buttons) {
					return fmt.Errorf("wizard: step %s: button %d of %d", st.Name, n, st.Buttons)
				}
				continue
			}
			fi = i - 1
		}
		if fi < len(st.Formats) {
			f := st.Formats[fi]
			if n < f.Min || n > f.Max {
				return fmt.Errorf("wizard: step %s: %s outside %s … %s", st.Name, f.Show(n), f.Show(f.Min), f.Show(f.Max))
			}
		}
	}
	return nil
}

// Back returns to the step answered last. Its answer is offered again.
func (s *Session) Back() (*Step, error) {
	if len(s.order) == 0 {
		return s.Current(), errors.New("wizard: at the first step")
	}
	k := s.order[len(s.order)-1]
	s.order = s.order[:len(s.order)-1]
	if s.kept == nil {
		s.kept = Answers{}
	}
	s.kept[k] = s.ans[k]
	delete(s.ans, k)
	return s.Start()
}

// Answers returns the values of every step the last assembly reached:
// those entered and, for the steps that did not wait, the values used.
func (s *Session) Answers() Answers {
	a := Answers{}
	if s.cur != nil {
		for _, st := range s.cur.Steps {
			a[st.Key] = st.Values
		}
	}
	for k, v := range s.ans {
		a[k] = v
	}
	return a
}

// Summary assembles the program with the answers in the summary state and
// returns the run: its steps marked Listed are the summary, in order.
func (s *Session) Summary() (*Run, error) {
	return s.p.Assemble(Options{State: Summary, Answers: s.Answers(), Defaults: s.Defaults})
}
