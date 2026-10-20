// Package asm is a multi-pass macro assembler with an expression
// calculator: the language in which assistant sources (.vll) are written
// (#32). The behaviour is reverse-engineered (#8) and restated in our own
// words in the project's spec; where the spec leaves a detail open, the
// choice made here is marked "inferred" in the comments.
//
// A source is line-oriented:
//
//	[label[(params)][:]] [mnemonic [operand, operand…]] [; comment]
//
// A line that starts with a character other than a space or a tab starts
// with a label (inferred from the composer's " .SOURCE file" lines, which
// start with a space). A mnemonic is a directive (.EQU, .MACRO, DATA, …), a
// macro defined in the source, or a directive the user of the package adds
// (Assembler.Directives; the assistant directives come in that way).
// Labels, mnemonics and function names are not case-sensitive (inferred).
//
// The program is the bytes that DATA emits; "$" is their count so far (the
// location counter). The source is assembled in passes until no label
// changes value, so a label may be used before it is defined: in a later
// pass it has the value of the previous one, which .IFDEF can tell apart.
package asm

import (
	"errors"
	"fmt"
	"strings"
)

// MaxWhile is the most times a .WHILE loop runs (reverse-engineered, #8).
const MaxWhile = 1024

// MaxPasses bounds the passes when labels keep changing.
const MaxPasses = 12

// maxDepth bounds nested macro calls, sources and .ASSEMBLE lines.
const maxDepth = 256

// ErrStop and ErrRestart may be returned by a Directive. ErrStop ends the
// pass at that line without an error; Assemble then returns ErrStop with
// the result of the last pass. ErrRestart ends the assembly at once and
// Assemble returns it.
var (
	ErrStop    = errors.New("assembly stopped")
	ErrRestart = errors.New("assembly restarted")
)

// errAbort ends a pass after a .FERROR.
var errAbort = errors.New("fatal error")

// Directive handles a mnemonic added to the assembler. label is the line's
// label ("" if none) and ops its operands as written, split at top-level
// commas. The assembler does not define the label.
type Directive func(c *Ctx, label string, ops []string) error

// Func is a calculator function added to the assembler.
type Func func(c *Ctx, args []Value) (Value, error)

// Assembler assembles a source. Set the fields before calling Assemble.
type Assembler struct {
	// Open returns the text of a file named by .INSERT or .SOURCE.
	Open func(name string) ([]byte, error)
	// Labels are global labels, defined at the start of each pass. The
	// source may change them as with .SET.
	Labels map[string]Value
	// Directives and Funcs extend the assembler. Their names are not
	// case-sensitive; directive names include the leading dot. They are
	// looked up before the built-in ones and the macros.
	Directives map[string]Directive
	Funcs      map[string]Func

	pass   int
	global map[string]*label
	spaces []*space            // label space of each .SOURCE, by order in the pass
	locals []map[string]*label // label space of each macro call, by order in the pass
	nspace int
	nlocal int
	macros map[string]*macro
	code   []byte
	last   []byte // code of the previous pass
	errs   []error
	msgs   []string
	frames []*frame
}

// Result is the outcome of an assembly.
type Result struct {
	Code     []byte
	Passes   int
	Messages []string // from .MESSG and .ERROR, in the last pass
	labels   *Assembler
}

// Label returns the value of a global label, or of a label of the main
// source, after the assembly.
func (r *Result) Label(name string) (Value, bool) {
	l := r.labels.global[key(name)]
	if l == nil && len(r.labels.spaces) > 0 {
		l = r.labels.spaces[0].labels[key(name)]
	}
	if l == nil || !l.known {
		return Value{}, false
	}
	return l.v, true
}

type label struct {
	v      Value
	known  bool // has a value
	pass   int  // the pass that last defined it
	stale  bool // the value was computed from a stale value
	fixed  bool // .EQU
	fn     *function
	pv     Value // value and knownness at the end of the previous pass
	pknown bool
}

type function struct {
	params []string
	body   string
}

type space struct {
	name   string // the .SOURCE file (the main source's name for its own)
	labels map[string]*label
	global bool // .GLOBAL True
}

type macro struct {
	min    int
	params []string // lower-case
	str    []bool   // s:param: the argument's text
	body   []srcLine
}

type srcLine struct {
	text string
	file string
	line int
}

func (l srcLine) pos() string { return fmt.Sprintf("%s:%d", l.file, l.line) }

type cond struct {
	active bool // lines are assembled
	taken  bool // a branch was taken
	outer  bool // the enclosing block is assembled
}

type loop struct {
	start int // index of the .WHILE line
	count int
}

type frame struct {
	lines []srcLine
	pc    int
	space *space
	local map[string]*label // macro call scope, or nil
	conds []cond
	loops []loop
}

func (f *frame) active() bool { return len(f.conds) == 0 || f.conds[len(f.conds)-1].active }

// PosError is an error at a line of the source.
type PosError struct {
	Pos string
	Err error
}

func (e *PosError) Error() string { return e.Pos + ": " + e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

func key(name string) string { return strings.ToLower(name) }

// Assemble assembles src, which is named name in messages. On an error the
// Result, if not nil, holds the last pass.
func (a *Assembler) Assemble(name string, src []byte) (*Result, error) {
	a.global = map[string]*label{}
	a.spaces, a.locals, a.last = nil, nil, nil
	lines := splitLines(name, src)
	var err error
	converged := false
	for a.pass = 1; a.pass <= MaxPasses; a.pass++ {
		a.code, a.errs, a.msgs, a.frames = nil, nil, nil, nil
		a.macros = map[string]*macro{}
		a.nspace, a.nlocal = 0, 0
		a.snapshot()
		for n, v := range a.Labels {
			a.set(a.lookupOrCreate(a.global, n), v, nil, false)
		}
		err = a.run(&frame{lines: lines, space: a.newSpace(name)})
		if errors.Is(err, ErrRestart) {
			return nil, err
		}
		if !a.changed() && a.pass > 1 {
			converged = true
			break
		}
		a.last = a.code
	}
	res := &Result{Code: a.code, Passes: min(a.pass, MaxPasses), Messages: a.msgs, labels: a}
	switch {
	case len(a.errs) > 0:
		return res, a.errs[0]
	case !converged:
		return res, fmt.Errorf("%s: labels still change after %d passes", name, MaxPasses)
	}
	return res, err // nil or ErrStop
}

// snapshot records each label's value at the end of the previous pass.
func (a *Assembler) snapshot() {
	a.each(func(l *label) { l.pv, l.pknown = l.v, l.known })
}

func (a *Assembler) each(f func(*label)) {
	for _, l := range a.global {
		f(l)
	}
	for _, s := range a.spaces {
		for _, l := range s.labels {
			f(l)
		}
	}
	for _, m := range a.locals {
		for _, l := range m {
			f(l)
		}
	}
}

// changed reports whether a label differs from the previous pass, or was
// defined in it and not in this one.
func (a *Assembler) changed() bool {
	ch := false
	a.each(func(l *label) {
		switch {
		case l.pass == a.pass:
			if l.known != l.pknown || l.known && !l.v.Equal(l.pv) {
				ch = true
			}
		case l.pass == a.pass-1 && l.known:
			ch = true
		}
	})
	return ch
}

// newSpace returns the label space of the next .SOURCE in this pass: the
// one in the same place in the previous pass.
func (a *Assembler) newSpace(name string) *space {
	if a.nspace == len(a.spaces) {
		a.spaces = append(a.spaces, &space{labels: map[string]*label{}})
	}
	s := a.spaces[a.nspace]
	s.name, s.global = name, false
	a.nspace++
	return s
}

func (a *Assembler) newLocal() map[string]*label {
	if a.nlocal == len(a.locals) {
		a.locals = append(a.locals, map[string]*label{})
	}
	m := a.locals[a.nlocal]
	a.nlocal++
	return m
}

func splitLines(file string, src []byte) []srcLine {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var out []srcLine
	for i, l := range strings.Split(text, "\n") {
		out = append(out, srcLine{text: l, file: file, line: i + 1})
	}
	return out
}

// run assembles the lines of root and of the frames it pushes.
func (a *Assembler) run(root *frame) error {
	a.frames = []*frame{root}
	for len(a.frames) > 0 {
		f := a.frames[len(a.frames)-1]
		if f.pc >= len(f.lines) {
			if len(f.conds) > 0 || len(f.loops) > 0 {
				l := f.lines[len(f.lines)-1]
				a.errs = append(a.errs, &PosError{l.pos(), fmt.Errorf("%w: .IF or .WHILE without its end", ErrSyntax)})
			}
			a.frames = a.frames[:len(a.frames)-1]
			continue
		}
		l := f.lines[f.pc]
		f.pc++
		err := a.line(f, l)
		switch {
		case err == nil:
		case errors.Is(err, ErrStop), errors.Is(err, ErrRestart):
			return err
		default:
			var pe *PosError
			if !errors.As(err, &pe) {
				err = &PosError{l.pos(), err}
			}
			a.errs = append(a.errs, err)
			if errors.Is(err, errAbort) {
				return nil
			}
		}
	}
	return nil
}

func (a *Assembler) push(f *frame) error {
	if len(a.frames) >= maxDepth {
		return fmt.Errorf("macros, sources or .ASSEMBLE nested more than %d deep", maxDepth)
	}
	a.frames = append(a.frames, f)
	return nil
}

// parseLine splits a line into label, mnemonic and operands, without the
// comment.
func parseLine(text string) (lbl, mnem, ops string) {
	text = stripComment(text)
	if text == "" {
		return
	}
	rest := text
	if c := text[0]; c != ' ' && c != '\t' && c != '.' {
		i, depth := 0, 0
		for ; i < len(text); i++ {
			c := text[i]
			if c == '(' {
				depth++
			} else if c == ')' {
				depth--
			} else if depth == 0 && (c == ' ' || c == '\t' || c == ':') {
				break
			}
		}
		lbl, rest = text[:i], text[i:]
		rest = strings.TrimPrefix(rest, ":")
	}
	rest = strings.TrimLeft(rest, " \t")
	i := strings.IndexAny(rest, " \t")
	if i < 0 {
		return lbl, rest, ""
	}
	return lbl, rest[:i], strings.TrimSpace(rest[i:])
}

func stripComment(s string) string {
	q := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			q = !q
		case ';':
			if !q {
				return strings.TrimRight(s[:i], " \t")
			}
		}
	}
	return strings.TrimRight(s, " \t")
}

// splitOps splits operands at commas outside brackets and strings.
func splitOps(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	depth, q, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			q = !q
		case q:
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// line assembles one line of f.
func (a *Assembler) line(f *frame, l srcLine) error {
	lbl, mnem, ops := parseLine(l.text)
	up := strings.ToUpper(mnem)
	if !f.active() {
		return a.skipped(f, up)
	}
	if mnem == "" {
		if lbl != "" {
			return a.defineHere(f, lbl)
		}
		return nil
	}
	c := &Ctx{a: a, f: f, pos: l.pos()}
	if d, ok := a.directive(up); ok {
		return d(c, lbl, splitOps(ops))
	}
	switch up {
	case ".EQU", ".SET":
		return a.equ(c, lbl, ops, up == ".EQU")
	case ".MACRO":
		return a.defMacro(f, lbl, ops)
	}
	if lbl != "" {
		if err := a.defineHere(f, lbl); err != nil {
			return err
		}
	}
	if b, ok := builtinDirectives[up]; ok {
		return b(a, c, ops)
	}
	if m := a.macros[key(mnem)]; m != nil {
		return a.callMacro(c, m, splitOps(ops))
	}
	return fmt.Errorf("%w: unknown mnemonic %s", ErrSyntax, mnem)
}

func (a *Assembler) directive(up string) (Directive, bool) {
	for n, d := range a.Directives {
		if strings.EqualFold(n, up) {
			return d, true
		}
	}
	return nil, false
}

// skipped handles a line in a block that is not assembled: only the
// conditionals count.
func (a *Assembler) skipped(f *frame, up string) error {
	switch up {
	case ".IF", ".IFNOT", ".IFDEF", ".IFNDEF":
		f.conds = append(f.conds, cond{taken: true})
	case ".ELSE":
		top := &f.conds[len(f.conds)-1]
		if top.outer && !top.taken {
			top.active, top.taken = true, true
		}
	case ".ENDIF":
		f.conds = f.conds[:len(f.conds)-1]
	}
	return nil
}

func (a *Assembler) defineHere(f *frame, lbl string) error {
	name, params, err := labelName(lbl)
	if err != nil {
		return err
	}
	if params != nil {
		return fmt.Errorf("%w: function label %s without .EQU", ErrSyntax, lbl)
	}
	return a.define(f, name, IntVal(int64(len(a.code))), nil, false, false)
}

// labelName splits "name(p1,p2)" into the name and parameters (nil for a
// plain label).
func labelName(lbl string) (string, []string, error) {
	i := strings.IndexByte(lbl, '(')
	if i < 0 {
		return lbl, nil, nil
	}
	if !strings.HasSuffix(lbl, ")") {
		return "", nil, fmt.Errorf("%w: bad label %s", ErrSyntax, lbl)
	}
	ps := splitOps(lbl[i+1 : len(lbl)-1])
	if ps == nil {
		ps = []string{}
	}
	for j := range ps {
		ps[j] = key(ps[j])
	}
	return lbl[:i], ps, nil
}

// find returns the label name in the scopes of f (innermost first), or nil.
func (a *Assembler) find(f *frame, fns []map[string]*label, name string) *label {
	k := key(name)
	for i := len(fns) - 1; i >= 0; i-- {
		if l := fns[i][k]; l != nil {
			return l
		}
	}
	if f != nil {
		if f.local != nil {
			if l := f.local[k]; l != nil {
				return l
			}
		}
		if l := f.space.labels[k]; l != nil {
			return l
		}
	}
	return a.global[k]
}

// target returns the label that a definition of name in f sets: the one
// already visible, or a new one in the current label space (global after
// .GLOBAL True).
func (a *Assembler) target(f *frame, fns []map[string]*label, name string) *label {
	if l := a.find(f, fns, name); l != nil {
		return l
	}
	m := f.space.labels
	if f.space.global {
		m = a.global
	}
	return a.lookupOrCreate(m, name)
}

func (a *Assembler) lookupOrCreate(m map[string]*label, name string) *label {
	k := key(name)
	l := m[k]
	if l == nil {
		l = &label{pass: -1}
		m[k] = l
	}
	return l
}

// set gives l a value (or makes it unknown when err is ErrUnknown).
func (a *Assembler) set(l *label, v Value, err error, fixed bool) {
	l.pass, l.fixed, l.fn = a.pass, fixed, nil
	if err != nil {
		l.known, l.v = false, Value{}
		return
	}
	l.known, l.v = true, v
}

// define sets name in f. An .EQU label may be set once per pass (again
// only to the same value).
func (a *Assembler) define(f *frame, name string, v Value, err error, stale, fixed bool) error {
	if name == "" || !isIdentStart(name[0]) {
		return fmt.Errorf("%w: bad label %q", ErrSyntax, name)
	}
	l := a.target(f, nil, name)
	if l.fixed && l.pass == a.pass && l.known && err == nil && !l.v.Equal(v) {
		return fmt.Errorf("label %s already defined", name)
	}
	a.set(l, v, err, fixed)
	l.stale = stale
	return err
}

// equ handles "name .EQU expr" and "name .SET expr[, width, signed]", and
// "name(p1, …) .EQU expr" for a function. Without a label the name may be
// the first operand.
func (a *Assembler) equ(c *Ctx, lbl, ops string, fixed bool) error {
	if lbl == "" {
		parts := splitOps(ops)
		if len(parts) < 2 {
			return fmt.Errorf("%w: label expected", ErrSyntax)
		}
		lbl, ops = parts[0], strings.Join(parts[1:], ",")
	}
	name, params, err := labelName(lbl)
	if err != nil {
		return err
	}
	if params != nil {
		l := a.target(c.f, nil, name)
		a.set(l, StrVal(ops), nil, fixed)
		l.fn = &function{params: params, body: ops}
		return nil
	}
	parts := []string{ops}
	if !fixed {
		parts = splitOps(ops)
		if len(parts) == 0 || len(parts) > 3 {
			return fmt.Errorf("%w: .SET name expr[, width, signed]", ErrSyntax)
		}
	}
	v, err := c.eval(parts[0])
	if err == nil && len(parts) > 1 {
		v, err = c.wrap(v, parts[1:])
	}
	if err != nil && !errors.Is(err, ErrUnknown) {
		return err
	}
	return a.define(c.f, name, v, err, c.stale, fixed)
}

// wrap limits v to width bits, sign-extended when signed is true
// (inferred: the meaning of .SET's optional width and signed operands).
func (c *Ctx) wrap(v Value, ops []string) (Value, error) {
	n, err := v.AsInt()
	if err != nil {
		return v, err
	}
	wv, err := c.eval(ops[0])
	if err != nil {
		return v, err
	}
	w, err := wv.AsInt()
	if err != nil || w < 1 || w > 63 {
		return v, fmt.Errorf("%w: bad width", ErrSyntax)
	}
	signed := false
	if len(ops) > 1 {
		sv, err := c.eval(ops[1])
		if err != nil {
			return v, err
		}
		if signed, err = sv.AsBool(); err != nil {
			return v, err
		}
	}
	n &= 1<<w - 1
	if signed && n&(1<<(w-1)) != 0 {
		n -= 1 << w
	}
	return IntVal(n), nil
}

// defMacro handles "name .MACRO [min], p1, s:p2, …" and collects the body
// up to the matching .ENDM.
func (a *Assembler) defMacro(f *frame, name, ops string) error {
	if name == "" {
		return fmt.Errorf("%w: .MACRO without a name", ErrSyntax)
	}
	m := &macro{}
	for i, p := range splitOps(ops) {
		if i == 0 {
			if v, _, err := scanNumber(p); err == nil && len(p) > 0 && p[0] >= '0' && p[0] <= '9' {
				n, _ := v.AsInt()
				m.min = int(n)
				continue
			}
		}
		s := len(p) > 2 && strings.EqualFold(p[:2], "s:")
		if s {
			p = p[2:]
		}
		if p == "" {
			continue
		}
		m.params = append(m.params, key(p))
		m.str = append(m.str, s)
	}
	depth := 0
	for ; f.pc < len(f.lines); f.pc++ {
		l := f.lines[f.pc]
		_, mn, _ := parseLine(l.text)
		switch strings.ToUpper(mn) {
		case ".MACRO":
			depth++
		case ".ENDM":
			if depth == 0 {
				f.pc++
				a.macros[key(name)] = m
				return nil
			}
			depth--
		}
		m.body = append(m.body, l)
	}
	return fmt.Errorf("%w: .MACRO %s without .ENDM", ErrSyntax, name)
}

// callMacro assembles the body of m with its parameters bound: a
// parameter is the value of its argument, or for s:param the argument's
// text; Params is the number of arguments (reverse-engineered, #8). A
// missing argument leaves its parameter unknown.
func (a *Assembler) callMacro(c *Ctx, m *macro, args []string) error {
	if len(args) < m.min {
		return fmt.Errorf("%w: %d arguments, at least %d expected", ErrSyntax, len(args), m.min)
	}
	local := a.newLocal()
	bind := func(name string, v Value, err error, stale bool) {
		l := a.lookupOrCreate(local, name)
		a.set(l, v, err, false)
		l.stale = stale
	}
	bind("params", IntVal(int64(len(args))), nil, false)
	for i, p := range m.params {
		switch {
		case i >= len(args):
			bind(p, Value{}, ErrUnknown, false)
		case m.str[i]:
			bind(p, StrVal(args[i]), nil, false)
		default:
			c.stale = false
			v, err := c.eval(args[i])
			if err != nil && !errors.Is(err, ErrUnknown) {
				return err
			}
			bind(p, v, err, c.stale)
		}
	}
	return a.push(&frame{lines: m.body, space: c.f.space, local: local})
}

type builtinDirective func(a *Assembler, c *Ctx, ops string) error

var builtinDirectives map[string]builtinDirective

func init() {
	builtinDirectives = map[string]builtinDirective{
		".NOP": nop, ".LIST": nop, ".NOLIST": nop, ".EXT": nop,
		// .DEFER/.NODEFER change where errors are reported (outside the
		// macro); errors here always carry the line, so they do nothing
		// (inferred).
		".DEFER": nop, ".NODEFER": nop,
		".ENDM": func(a *Assembler, c *Ctx, ops string) error {
			return fmt.Errorf("%w: .ENDM without .MACRO", ErrSyntax)
		},
		".UNSET": func(a *Assembler, c *Ctx, ops string) error {
			for _, n := range splitOps(ops) {
				if l := a.find(c.f, nil, n); l != nil {
					a.set(l, Value{}, ErrUnknown, false)
				}
			}
			return nil
		},
		".RENMACRO": func(a *Assembler, c *Ctx, ops string) error {
			p := splitOps(ops)
			if len(p) != 2 {
				return fmt.Errorf("%w: .RENMACRO old, new", ErrSyntax)
			}
			m := a.macros[key(p[0])]
			if m == nil {
				return fmt.Errorf("no macro %s", p[0])
			}
			delete(a.macros, key(p[0]))
			if key(p[0]) != key(p[1]) {
				a.macros[key(p[1])] = m
			}
			return nil
		},
		".IF":     ifDirective(false, false),
		".IFNOT":  ifDirective(true, false),
		".IFDEF":  ifDirective(false, true),
		".IFNDEF": ifDirective(true, true),
		".ELSE": func(a *Assembler, c *Ctx, ops string) error {
			if len(c.f.conds) == 0 {
				return fmt.Errorf("%w: .ELSE without .IF", ErrSyntax)
			}
			top := &c.f.conds[len(c.f.conds)-1]
			top.active = false
			return nil
		},
		".ENDIF": func(a *Assembler, c *Ctx, ops string) error {
			if len(c.f.conds) == 0 {
				return fmt.Errorf("%w: .ENDIF without .IF", ErrSyntax)
			}
			c.f.conds = c.f.conds[:len(c.f.conds)-1]
			return nil
		},
		".WHILE": whileDirective,
		".ENDW": func(a *Assembler, c *Ctx, ops string) error {
			if len(c.f.loops) == 0 {
				return fmt.Errorf("%w: .ENDW without .WHILE", ErrSyntax)
			}
			c.f.pc = c.f.loops[len(c.f.loops)-1].start
			return nil
		},
		".LOCAL": func(a *Assembler, c *Ctx, ops string) error {
			if c.f.local == nil {
				return nil
			}
			for _, n := range splitOps(ops) {
				a.lookupOrCreate(c.f.local, n)
			}
			return nil
		},
		".PUB": func(a *Assembler, c *Ctx, ops string) error {
			for _, n := range splitOps(ops) {
				k := key(n)
				if l := c.f.space.labels[k]; l != nil && a.global[k] == nil {
					a.global[k] = l
					delete(c.f.space.labels, k)
				} else if l == nil && a.global[k] == nil {
					a.global[k] = &label{pass: -1}
				}
			}
			return nil
		},
		".GLOBAL": func(a *Assembler, c *Ctx, ops string) error {
			v, err := c.eval(ops)
			if err != nil {
				return err
			}
			b, err := v.AsBool()
			c.f.space.global = b
			return err
		},
		"DATA": func(a *Assembler, c *Ctx, ops string) error {
			for _, op := range splitOps(ops) {
				v, err := c.eval(op)
				if err != nil {
					a.code = append(a.code, 0) // keep the location counter
					return err
				}
				if err := a.emit(v); err != nil {
					return err
				}
			}
			return nil
		},
		".MESSG":  message(false, false),
		".ERROR":  message(true, false),
		".FERROR": message(true, true),
		".ASSEMBLE": func(a *Assembler, c *Ctx, ops string) error {
			v, err := c.eval(ops)
			if err != nil {
				return err
			}
			if v.K != Str {
				return fmt.Errorf("%w: .ASSEMBLE needs a string", ErrType)
			}
			lines := splitLines(c.pos, []byte(v.S))
			for i := range lines {
				lines[i].file, lines[i].line = c.pos+" .ASSEMBLE", i+1
			}
			return a.push(&frame{lines: lines, space: c.f.space, local: c.f.local})
		},
		".INSERT": include(false),
		".SOURCE": include(true),
	}
}

func nop(a *Assembler, c *Ctx, ops string) error { return nil }

// emit appends the low byte of an integer or boolean, each character of a
// string, or each element of a list (inferred for strings and lists).
func (a *Assembler) emit(v Value) error {
	switch v.K {
	case Int, Bool:
		a.code = append(a.code, byte(v.I))
	case Str:
		a.code = append(a.code, v.S...)
	case List:
		for _, e := range v.L {
			if err := a.emit(e); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: DATA of %s", ErrType, v.kindName())
	}
	return nil
}

// ifDirective makes .IF (expr is true), .IFNOT, .IFDEF (expr can be
// calculated with the values of this pass) and .IFNDEF.
func ifDirective(invert, def bool) builtinDirective {
	return func(a *Assembler, c *Ctx, ops string) error {
		c.stale = false
		v, err := c.eval(ops)
		var ok bool
		if def {
			ok = err == nil && !c.stale
			err = nil
		} else if err == nil {
			ok, err = v.AsBool()
		}
		ok = ok != invert
		c.f.conds = append(c.f.conds, cond{active: ok, taken: ok, outer: true})
		return err
	}
}

func whileDirective(a *Assembler, c *Ctx, ops string) error {
	f := c.f
	start := f.pc - 1
	var lp *loop
	if n := len(f.loops); n > 0 && f.loops[n-1].start == start {
		lp = &f.loops[n-1]
		lp.count++
	}
	v, err := c.eval(ops)
	ok := false
	if err == nil {
		ok, err = v.AsBool()
	}
	if ok && lp != nil && lp.count >= MaxWhile {
		ok, err = false, fmt.Errorf(".WHILE ran %d times", MaxWhile)
	}
	if ok {
		if lp == nil {
			f.loops = append(f.loops, loop{start: start})
		}
		return nil
	}
	if lp != nil {
		f.loops = f.loops[:len(f.loops)-1]
	}
	// Skip to after the matching .ENDW.
	depth := 0
	for ; f.pc < len(f.lines); f.pc++ {
		_, mn, _ := parseLine(f.lines[f.pc].text)
		switch strings.ToUpper(mn) {
		case ".WHILE":
			depth++
		case ".ENDW":
			if depth == 0 {
				f.pc++
				return err
			}
			depth--
		}
	}
	return fmt.Errorf("%w: .WHILE without .ENDW", ErrSyntax)
}

// message makes .MESSG, .ERROR and .FERROR. The operand is a string
// expression, or taken as written when it is not one (inferred).
func message(isErr, fatal bool) builtinDirective {
	return func(a *Assembler, c *Ctx, ops string) error {
		text := ops
		if v, err := c.eval(ops); err == nil {
			text = v.String()
		}
		a.msgs = append(a.msgs, text)
		switch {
		case fatal:
			return fmt.Errorf("%w: %w", errAbort, &UserError{Msg: text})
		case isErr:
			return &UserError{Msg: text}
		}
		return nil
	}
}

// include makes .INSERT (the file's lines as if written here) and .SOURCE
// (the same, in a label space of its own). The file name is written as is,
// or as a string expression in quotes.
func include(own bool) builtinDirective {
	return func(a *Assembler, c *Ctx, ops string) error {
		name := strings.TrimSpace(ops)
		if strings.HasPrefix(name, "'") {
			v, err := c.eval(name)
			if err != nil {
				return err
			}
			name = v.String()
		}
		if a.Open == nil {
			return fmt.Errorf("cannot open %s", name)
		}
		src, err := a.Open(name)
		if err != nil {
			return err
		}
		sp, local := c.f.space, c.f.local
		if own {
			sp, local = a.newSpace(name), nil
		}
		return a.push(&frame{lines: splitLines(name, src), space: sp, local: local})
	}
}
