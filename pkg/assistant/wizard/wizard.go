// Package wizard is the assistant layer over the assembler (#32): the
// assistant directives (.AID, .AUE, .ALD, …), the read-back functions, and
// the step model a user interface drives (start, current step, answer,
// back, summary). It has no user interface of its own.
//
// The wizard is not described separately from the code: an assistant's
// source declares its steps (.AID), asks for values (.AUE) and binds them
// to labels, which the rest of the source turns into code. A step is shown
// by assembling the program until the first step that has no answer yet
// (reverse-engineered, #8; restated in our own words). Each answer makes
// the program assemble again from the start, so later steps can depend on
// earlier answers.
package wizard

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/bolchisb/openveconfig/pkg/assistant/asm"
)

// States of the assistant assembler, as the source sees them in
// $AssemblerState (reverse-engineered, #8).
const (
	DeveloperBuild = iota
	TranslatorBuild
	BackReadBuild
	DefaultBuild
	Build
	UserEntry
	Summary
)

// maxRestarts bounds .RESTART loops.
const maxRestarts = 16

// Program is a source to assemble with the assistant directives.
type Program struct {
	Main   string // name of the main source, for messages
	Source []byte
	// Open returns a file named by .SOURCE or .INSERT.
	Open func(name string) ([]byte, error)
	// Labels is the environment the composer defines (device, settings).
	Labels map[string]asm.Value
	// Texts holds each assistant's text, by the name it is sourced under.
	Texts map[string]Text
}

// Key names a step: the source it is declared in and its label (lower
// case).
type Key struct{ Source, Step string }

// Answers are the values entered at each step, in the order of the step's
// labels. A radio step's value is the index of the chosen button.
type Answers map[Key][]asm.Value

// Step is a wizard step reached while assembling.
type Step struct {
	Key
	Directive string // .AUE, .AIN (input), .AIP (information), .AER (error)
	Name      string // selects the text section
	Type      byte   // 'E' entry, 'R' radio buttons, 'C' check/confirm
	Listed    bool   // shown in the summary
	Buttons   int    // R and C: the number of buttons
	Default   int    // R and C: the default button
	Formats   []Format
	Labels    []string    // the labels the values are bound to
	Values    []asm.Value // the values used, or offered when the step waits
	Text      string      // the text section of Name
	Pos       string      // where in the source
}

// Options control one assembly.
type Options struct {
	State int
	// Answers are the values to use at those steps.
	Answers Answers
	// Defaults are offered at steps without an answer, before the step's
	// own default; at a step that does not wait they are used as answers.
	Defaults Answers
	// Edit, with UserEntry, makes only the steps of that source wait for
	// the user (all of them when empty).
	Edit string
	// Image is the image that read-back functions read (BackReadBuild).
	Image []byte
}

// Run is the outcome of one assembly.
type Run struct {
	Code     []byte
	Steps    []*Step // the steps reached, in order
	Pending  *Step   // the step that waits for the user, or nil
	Messages []string
	Result   *asm.Result
}

// Assemble assembles p once.
func (p *Program) Assemble(o Options) (*Run, error) {
	for range maxRestarts {
		run, err := p.assemble(o)
		if errors.Is(err, asm.ErrRestart) {
			continue
		}
		return run, err
	}
	return nil, fmt.Errorf("%s: .RESTART more than %d times", p.Main, maxRestarts)
}

// run state of one assembly.
type assembly struct {
	p     *Program
	o     Options
	pass  int
	defs  map[Key]*Step
	n     int // steps declared in this pass
	run   *Run
	bound map[string]asm.Value // label values entered, for AssistantLabelValue
}

func (p *Program) assemble(o Options) (*Run, error) {
	s := &assembly{p: p, o: o}
	labels := map[string]asm.Value{}
	for k, v := range p.Labels {
		labels[k] = v
	}
	// The state labels (reverse-engineered, #8). Only $AssemblerState
	// and $AssemblerPass are known in meaning; the others start as
	// below (unknown: their values in the original).
	labels["$AssemblerState"] = asm.IntVal(int64(o.State))
	for _, n := range []string{"$AUEHandled", "$AutoNext", "$ForceDefaultOnAutoNext", "$AssistantCancelled"} {
		labels[n] = asm.BoolVal(false)
	}
	labels["$AssistantStep"] = asm.IntVal(0)
	labels["$AssistantDisplayStep"] = asm.IntVal(0)
	// $HandledAUEStep names the step whose .AUE was handled: a source
	// joins it to text to build an expression over that step's label,
	// so it is a string. Nothing here marks a step handled yet, which
	// leaves it empty (unknown: how the original sets it).
	labels["$HandledAUEStep"] = asm.StrVal("")
	labels["$AssistantBackStepList"] = asm.ListVal()
	a := &asm.Assembler{
		Open:   p.Open,
		Labels: labels,
		Directives: map[string]asm.Directive{
			".AID": s.aid, ".AUE": s.input(".AUE"), ".AIN": s.input(".AIN"),
			".AIP": s.info(".AIP"), ".AER": s.info(".AER"),
			".ALD": s.ald, ".ALS": s.als,
			".AMESSG": s.amessg, ".AERROR": s.aerror,
			".RESTART": func(*asm.Ctx, string, []string) error { return asm.ErrRestart },
		},
		Funcs: s.funcs(),
	}
	res, err := a.Assemble(p.Main, p.Source)
	if errors.Is(err, asm.ErrRestart) {
		return nil, err
	}
	run := s.run
	if run == nil {
		run = &Run{}
	}
	if res != nil {
		run.Code, run.Messages, run.Result = res.Code, res.Messages, res
	}
	if errors.Is(err, asm.ErrStop) {
		err = nil
	} else {
		run.Pending = nil
	}
	return run, err
}

// begin resets the per-pass state at the first directive of a pass.
func (s *assembly) begin(c *asm.Ctx) {
	if c.Pass() != s.pass {
		s.pass = c.Pass()
		s.defs, s.n = map[Key]*Step{}, 0
		s.run = &Run{}
		s.bound = map[string]asm.Value{}
	}
}

func key(c *asm.Ctx, step string) Key {
	return Key{Source: c.Source(), Step: strings.ToLower(strings.TrimSpace(step))}
}

// aid handles "step .AID Name, Type[, buttons, default], format…".
func (s *assembly) aid(c *asm.Ctx, lbl string, ops []string) error {
	s.begin(c)
	if lbl == "" || len(ops) < 2 {
		return fmt.Errorf("%w: step .AID name, type, …", asm.ErrSyntax)
	}
	t := strings.ToUpper(ops[1])
	if t == "" || !strings.ContainsRune("ERC", rune(t[0])) || len(t) > 2 || len(t) == 2 && t[1] != 'L' {
		return errors.New("Type must be {E|R|C}") // the original's check (reverse-engineered, #8)
	}
	st := &Step{Key: key(c, lbl), Name: ops[0], Type: t[0], Listed: len(t) == 2, Pos: c.Pos()}
	st.Text = s.p.Texts[c.Source()].Section(st.Name)
	rest := ops[2:]
	if st.Type != 'E' {
		if len(rest) < 2 {
			return fmt.Errorf("%w: %s step needs buttons and default", asm.ErrSyntax, t)
		}
		n, err := s.int(c, rest[0])
		if err != nil {
			return err
		}
		d, err := s.int(c, rest[1])
		if err != nil {
			return err
		}
		if n < 1 || d < 0 || d >= n {
			return fmt.Errorf("default button %d of %d", d, n)
		}
		st.Buttons, st.Default, rest = int(n), int(d), rest[2:]
	}
	for _, f := range rest {
		v, _, err := c.Eval(f)
		if err != nil {
			return err
		}
		fm, err := ParseFormat(v.String())
		if err != nil {
			return err
		}
		st.Formats = append(st.Formats, fm)
	}
	s.n++
	s.defs[st.Key] = st
	// .AID defines no label of its own: the step's label is given its
	// value by the .AUE that shows the step (reverse-engineered, #32).
	return nil
}

func (s *assembly) int(c *asm.Ctx, expr string) (int64, error) {
	v, _, err := c.Eval(expr)
	if err != nil {
		return 0, err
	}
	return v.AsInt()
}

// step returns a copy of the declared step named by op.
func (s *assembly) step(c *asm.Ctx, directive, op string) (*Step, error) {
	s.begin(c)
	def := s.defs[key(c, op)]
	if def == nil {
		return nil, fmt.Errorf("%s: no .AID for step %s", directive, op)
	}
	st := *def
	st.Directive, st.Pos = directive, c.Pos()
	return &st, nil
}

// waits reports whether st waits for the user in this assembly.
func (s *assembly) waits(st *Step) bool {
	if s.o.State != UserEntry || s.o.Edit != "" && s.o.Edit != st.Source {
		return false
	}
	_, answered := s.o.Answers[st.Key]
	return !answered
}

// input handles ".AUE step, label…" (and .AIN): the values of the step's
// labels come from the answers, or wait for the user.
func (s *assembly) input(directive string) asm.Directive {
	return func(c *asm.Ctx, lbl string, ops []string) error {
		if len(ops) == 0 {
			return fmt.Errorf("%w: %s step, label…", asm.ErrSyntax, directive)
		}
		st, err := s.step(c, directive, ops[0])
		if err != nil {
			return err
		}
		st.Labels = ops[1:]
		reached := s.defaults(c, st)
		vals, ok := s.o.Answers[st.Key]
		if !ok {
			vals = reached
		}
		st.Values = vals
		s.run.Steps = append(s.run.Steps, st)
		if s.waits(st) {
			s.run.Pending = st
			return asm.ErrStop
		}
		if err := s.defineStep(c, ops[0], reached, vals); err != nil {
			return err
		}
		for i, l := range st.Labels {
			if i < len(vals) {
				if err := c.Define(l, vals[i]); err != nil {
					return err
				}
				s.bound[strings.ToLower(l)] = vals[i]
			}
		}
		return nil
	}
}

// defineStep gives a step's label the three lists the sources index with
// @ (reverse-engineered, #32): the values the step held before this pass,
// the ones it was reached with and the ones it ends with, each a list of
// one integer per field, counted from 0. A source compares @1 with @2 to
// see whether the step changed anything, and reads @2 to see where it
// ended up.
//
// The first list should hold what the step held before — the value .ALD
// read back, or the format's default. Here it is the values the step was
// reached with, which is the same in every state but a read-back; no
// shipped source indexes it (they use only @1 and @2).
func (s *assembly) defineStep(c *asm.Ctx, name string, reached, final []asm.Value) error {
	// Reassign, not Define: a source may use the step's name for
	// something else too, as one that registers the function it provides
	// with AssistantConflictCheck does.
	return c.Reassign(name, asm.ListVal(fields(reached), fields(reached), fields(final)))
}

// fields is one of the step label's inner lists: a step's values as
// integers, which is how the sources read them.
func fields(vals []asm.Value) asm.Value {
	l := make([]asm.Value, 0, len(vals))
	for _, v := range vals {
		n, err := v.AsInt()
		if err != nil {
			n = 0
		}
		l = append(l, asm.IntVal(n))
	}
	return asm.ListVal(l...)
}

// defaults returns the values offered at st: Options.Defaults, else each
// label's current value (from .ALS or .ALD), else the step's default
// button or the format's default.
func (s *assembly) defaults(c *asm.Ctx, st *Step) []asm.Value {
	if v, ok := s.o.Defaults[st.Key]; ok {
		return v
	}
	var vals []asm.Value
	for i, l := range st.Labels {
		v, err := c.Lookup(l)
		switch {
		case err == nil:
		case st.Type != 'E' && i == 0:
			v = asm.IntVal(int64(st.Default))
		case st.Type == 'E' && i < len(st.Formats):
			v = asm.IntVal(st.Formats[i].Default)
		case st.Type != 'E' && i-1 < len(st.Formats):
			v = asm.IntVal(st.Formats[i-1].Default)
		default:
			return vals
		}
		vals = append(vals, v)
	}
	return vals
}

// info handles ".AIP step, value…" (information) and ".AER step, value…"
// (error): the step shows values and has no input. An information step
// waits for the user once; an error step stops a build (Build or
// DefaultBuild; in other states it is only listed).
func (s *assembly) info(directive string) asm.Directive {
	return func(c *asm.Ctx, lbl string, ops []string) error {
		if len(ops) == 0 {
			return fmt.Errorf("%w: %s step, value…", asm.ErrSyntax, directive)
		}
		st, err := s.step(c, directive, ops[0])
		if err != nil {
			return err
		}
		for _, op := range ops[1:] {
			v, _, err := c.Eval(op)
			if err != nil {
				return err
			}
			st.Values = append(st.Values, v)
		}
		s.run.Steps = append(s.run.Steps, st)
		switch {
		case s.waits(st):
			s.run.Pending = st
			return asm.ErrStop
		case directive == ".AER" && (s.o.State == Build || s.o.State == DefaultBuild):
			return &asm.UserError{Msg: "assistant error at step " + st.Name}
		}
		return nil
	}
}

// ald handles "label .ALD expr": in a read-back build the label gets the
// value read from the image.
func (s *assembly) ald(c *asm.Ctx, lbl string, ops []string) error {
	if s.o.State != BackReadBuild {
		return nil
	}
	if lbl == "" || len(ops) != 1 {
		return fmt.Errorf("%w: label .ALD expr", asm.ErrSyntax)
	}
	v, _, err := c.Eval(ops[0])
	if err != nil {
		return err
	}
	return c.Define(lbl, v)
}

// als handles "label .ALS value": an integer sets the label unless it has
// a value; D, N, P or X select where a default comes from
// (reverse-engineered, #8), which is not known and is ignored here.
func (s *assembly) als(c *asm.Ctx, lbl string, ops []string) error {
	if lbl == "" || len(ops) != 1 {
		return fmt.Errorf("%w: label .ALS value", asm.ErrSyntax)
	}
	if alsMode(ops[0]) {
		return nil
	}
	v, _, err := c.Eval(ops[0])
	if err != nil {
		return err
	}
	// The sources write the mode letter quoted as well as bare.
	if v.K == asm.Str && alsMode(v.S) {
		return nil
	}
	if v.K != asm.Int {
		return fmt.Errorf(".ALS value must be an integer or D, N, P, X")
	}
	if _, err := c.Lookup(lbl); err == nil {
		return nil
	}
	return c.Define(lbl, v)
}

// alsMode reports whether v is one of the letters .ALS takes in place of
// a value to say where the default comes from.
func alsMode(v string) bool {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case "D", "N", "P", "X":
		return true
	}
	return false
}

func (s *assembly) text(c *asm.Ctx, ops []string) string {
	if len(ops) == 0 {
		return ""
	}
	if v, _, err := c.Eval(strings.Join(ops, ",")); err == nil {
		return v.String()
	}
	return strings.Join(ops, ",")
}

func (s *assembly) amessg(c *asm.Ctx, lbl string, ops []string) error {
	c.Message(s.text(c, ops))
	return nil
}

func (s *assembly) aerror(c *asm.Ctx, lbl string, ops []string) error {
	return &asm.UserError{Msg: s.text(c, ops)}
}

// funcs are the read-back and text functions (reverse-engineered, #8):
// RB1/RB2/RB3 read a byte, a word or three bytes of the image, low byte
// first — a package that writes a value as three bytes binds its label
// with RB3 at the same offset — RBO the operand of the
// instruction at an offset, RB_<mnemonic> whether the opcode there is
// that instruction; AssistantText returns a text section, and
// AssistantLabelValue a value entered at an earlier step.
func (s *assembly) funcs() map[string]asm.Func {
	// absent reports that there is no image to read back, so a read gives
	// zero: that is a fresh build of an assistant the unit does not hold
	// yet, where the RC… comparisons the sources build on the image are
	// meant to find nothing. The read-back build itself cannot do without
	// the image and says so.
	absent := func() (bool, error) {
		switch {
		case s.o.Image != nil:
			return false, nil
		case s.o.State == BackReadBuild:
			return true, fmt.Errorf("%w: no image to read back", asm.ErrUnknown)
		}
		return true, nil
	}
	img := func(c *asm.Ctx, args []asm.Value, n int) (int, error) {
		if len(args) != 1 {
			return 0, fmt.Errorf("%w: one offset", asm.ErrSyntax)
		}
		x, err := args[0].AsInt()
		if err != nil {
			return 0, err
		}
		if x < 0 || int(x)+n > len(s.o.Image) {
			return 0, fmt.Errorf("read-back offset %d outside the image", x)
		}
		return int(x), nil
	}
	f := map[string]asm.Func{
		"RB1": func(c *asm.Ctx, a []asm.Value) (asm.Value, error) {
			if no, err := absent(); no {
				return asm.IntVal(0), err
			}
			x, err := img(c, a, 1)
			if err != nil {
				return asm.Value{}, err
			}
			return asm.IntVal(int64(s.o.Image[x])), nil
		},
		"RB2": func(c *asm.Ctx, a []asm.Value) (asm.Value, error) {
			if no, err := absent(); no {
				return asm.IntVal(0), err
			}
			x, err := img(c, a, 2)
			if err != nil {
				return asm.Value{}, err
			}
			return asm.IntVal(int64(binary.LittleEndian.Uint16(s.o.Image[x:]))), nil
		},
		"RB3": func(c *asm.Ctx, a []asm.Value) (asm.Value, error) {
			if no, err := absent(); no {
				return asm.IntVal(0), err
			}
			x, err := img(c, a, 3)
			if err != nil {
				return asm.Value{}, err
			}
			b := s.o.Image[x:]
			return asm.IntVal(int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16), nil
		},
		"RBO": func(c *asm.Ctx, a []asm.Value) (asm.Value, error) {
			if no, err := absent(); no {
				return asm.IntVal(0), err
			}
			x, err := img(c, a, 1)
			if err != nil {
				return asm.Value{}, err
			}
			v, err := operandAt(s.o.Image, x)
			return asm.IntVal(int64(v)), err
		},
		"AssistantText": func(c *asm.Ctx, a []asm.Value) (asm.Value, error) {
			if len(a) != 1 {
				return asm.Value{}, fmt.Errorf("%w: AssistantText(tag)", asm.ErrSyntax)
			}
			return asm.StrVal(s.p.Texts[c.Source()].Section(a[0].String())), nil
		},
		"AssistantLabelValue": func(c *asm.Ctx, a []asm.Value) (asm.Value, error) {
			if len(a) != 1 {
				return asm.Value{}, fmt.Errorf("%w: AssistantLabelValue(label)", asm.ErrSyntax)
			}
			v, ok := s.bound[strings.ToLower(a[0].String())]
			if !ok {
				return asm.Value{}, fmt.Errorf("%w %s: not entered", asm.ErrUnknown, a[0].String())
			}
			return v, nil
		},
	}
	for name := range opcodes {
		f["RB_"+name] = func(c *asm.Ctx, a []asm.Value) (asm.Value, error) {
			if no, err := absent(); no {
				return asm.BoolVal(Is(name, 0)), err
			}
			x, err := img(c, a, 1)
			if err != nil {
				return asm.Value{}, err
			}
			return asm.BoolVal(Is(name, s.o.Image[x])), nil
		}
	}
	return f
}

// ReadBack assembles p against an existing image (BackReadBuild) and
// returns the values its steps get from it: the user's entries as far as
// the source's .ALD lines recover them. The run's code should equal the
// image's program when the read-back is complete.
func (p *Program) ReadBack(img []byte) (Answers, *Run, error) {
	run, err := p.Assemble(Options{State: BackReadBuild, Image: img})
	if err != nil {
		return nil, run, err
	}
	ans := Answers{}
	for _, st := range run.Steps {
		ans[st.Key] = st.Values
	}
	return ans, run, nil
}
