package asm

import (
	"fmt"
	"strings"
)

// Ctx is the assembler as a Directive or Func sees it: the line being
// assembled and its label scopes.
type Ctx struct {
	a     *Assembler
	f     *frame
	pos   string
	fns   []map[string]*label // parameters of the user functions being called
	stale bool                // a stale label value was used since reset
	depth int
}

// Pos returns the file and line being assembled.
func (c *Ctx) Pos() string { return c.pos }

// Pass returns the pass number, from 1.
func (c *Ctx) Pass() int { return c.a.pass }

// PC returns the location counter: the number of bytes emitted.
func (c *Ctx) PC() int { return len(c.a.code) }

// LastCode returns the code of the previous pass (nil in the first).
func (c *Ctx) LastCode() []byte { return c.a.last }

// Eval evaluates an expression in the line's scope. stale reports that a
// label's value from an earlier pass was used.
func (c *Ctx) Eval(expr string) (v Value, stale bool, err error) {
	c.stale = false
	v, err = c.eval(expr)
	return v, c.stale, err
}

func (c *Ctx) eval(expr string) (Value, error) { return evalExpr(c, expr) }

// Define sets a variable label in the line's scope, as .SET does.
func (c *Ctx) Define(name string, v Value) error {
	return c.a.define(c.f, name, v, nil, false, false)
}

// Undefine makes a label unknown, as .UNSET does.
func (c *Ctx) Undefine(name string) {
	if l := c.a.find(c.f, nil, name); l != nil {
		c.a.set(l, Value{}, ErrUnknown, false)
	}
}

// Lookup returns a label's value in the line's scope.
func (c *Ctx) Lookup(name string) (Value, error) { return c.lookup(name) }

// Message adds a message to the result, as .MESSG does.
func (c *Ctx) Message(text string) { c.a.msgs = append(c.a.msgs, text) }

func (c *Ctx) lookup(name string) (Value, error) {
	switch key(name) {
	case "$":
		return IntVal(int64(len(c.a.code))), nil
	case "$code":
		return StrVal(string(c.a.code)), nil
	case "$listing":
		return StrVal(""), nil
	case "$assemblerpass":
		return IntVal(int64(c.a.pass)), nil
	}
	l := c.a.find(c.f, c.fns, name)
	if l == nil || !l.known {
		return Value{}, fmt.Errorf("%w %s", ErrUnknown, name)
	}
	if l.pass < c.a.pass || l.stale {
		c.stale = true
	}
	return l.v, nil
}

func (c *Ctx) assign(name string, v Value) error {
	l := c.a.target(c.f, c.fns, name)
	c.a.set(l, v, nil, false)
	l.stale = c.stale
	return nil
}

// call calls an added Func or a function label (name(p1, …) .EQU expr).
func (c *Ctx) call(name string, args []Value) (Value, bool, error) {
	for n, fn := range c.a.Funcs {
		if strings.EqualFold(n, name) {
			v, err := fn(c, args)
			return v, true, err
		}
	}
	l := c.a.find(c.f, c.fns, name)
	if l == nil || l.fn == nil {
		return Value{}, false, nil
	}
	if l.pass < c.a.pass {
		c.stale = true
	}
	if len(args) > len(l.fn.params) {
		return Value{}, true, fmt.Errorf("%w: %s takes %d arguments", ErrSyntax, name, len(l.fn.params))
	}
	if c.depth >= maxDepth {
		return Value{}, true, fmt.Errorf("%s: calls nested more than %d deep", name, maxDepth)
	}
	m := map[string]*label{}
	for i, p := range l.fn.params {
		pl := &label{pass: c.a.pass}
		if i < len(args) {
			pl.v, pl.known = args[i], true
		}
		m[p] = pl
	}
	c.fns = append(c.fns, m)
	c.depth++
	v, err := evalExpr(c, l.fn.body)
	c.depth--
	c.fns = c.fns[:len(c.fns)-1]
	return v, true, err
}
