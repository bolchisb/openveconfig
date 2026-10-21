package asm

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Errors of the calculator and the assembler wrap one of these.
var (
	ErrSyntax  = errors.New("syntax error")
	ErrType    = errors.New("type mismatch")
	ErrDivZero = errors.New("Division by ZERO") // the original's wording (reverse-engineered, #8)
	// ErrUnknown means an expression uses a label that has no value yet.
	// In an early pass that is expected (a forward reference).
	ErrUnknown = errors.New("unknown label")
)

type tokKind uint8

const (
	tEOF tokKind = iota
	tNum
	tStr
	tIdent
	tOp
)

type token struct {
	k tokKind
	s string // identifier, operator, or string contents
	v Value  // tNum
}

// isIdentStart and isIdentPart are the identifier characters. The original
// takes letters, digits and '_' (reverse-engineered, #8); sources also use
// '$' and '#' inside names, so they are accepted here too (inferred).
func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || c == '#' || '0' <= c && c <= '9'
}

var wordOps = map[string]bool{"OR": true, "MOD": true, "AND": true, "XOR": true}

// tokenize splits an expression into tokens.
func tokenize(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '\'' || c == '#':
			str, n, err := scanString(s[i:])
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{k: tStr, s: str})
			i += n
		case '0' <= c && c <= '9':
			v, n, err := scanNumber(s[i:])
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{k: tNum, v: v})
			i += n
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			w := s[i:j]
			if wordOps[strings.ToUpper(w)] {
				toks = append(toks, token{k: tOp, s: strings.ToUpper(w)})
			} else {
				toks = append(toks, token{k: tIdent, s: w})
			}
			i = j
		default:
			op := ""
			if i+1 < len(s) {
				switch s[i : i+2] {
				case "<=", ">=", "!=", "<<", ">>", ":=":
					op = s[i : i+2]
				}
			}
			if op == "" {
				if !strings.ContainsRune("^*/-+=<>!@()[],", rune(c)) {
					return nil, fmt.Errorf("%w: unexpected %q", ErrSyntax, c)
				}
				op = s[i : i+1]
			}
			toks = append(toks, token{k: tOp, s: op})
			i += len(op)
		}
	}
	return append(toks, token{k: tEOF}), nil
}

// scanString reads a string literal: quoted parts (in which two quotes
// stand for one) and character escapes #nn (decimal) or #$hh (hex), next
// to each other (reverse-engineered, #8).
func scanString(s string) (string, int, error) {
	var b strings.Builder
	i := 0
	for i < len(s) {
		switch s[i] {
		case '\'':
			i++
			for {
				if i >= len(s) {
					return "", 0, fmt.Errorf("%w: unterminated string", ErrSyntax)
				}
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						b.WriteByte('\'')
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(s[i])
				i++
			}
		case '#':
			j, base := i+1, 10
			if j < len(s) && s[j] == '$' {
				j, base = j+1, 16
			}
			k := j
			for k < len(s) && digitOK(s[k], base) {
				k++
			}
			n, err := strconv.ParseUint(s[j:k], base, 8)
			if err != nil {
				return "", 0, fmt.Errorf("%w: bad character escape %q", ErrSyntax, s[i:k])
			}
			b.WriteByte(byte(n))
			i = k
		default:
			return b.String(), i, nil
		}
	}
	return b.String(), i, nil
}

// scanNumber reads a decimal, 0x hex, 0b binary or decimal-point number.
func scanNumber(s string) (Value, int, error) {
	j := 0
	base := 10
	if len(s) > 1 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		base, j = 16, 2
	} else if len(s) > 1 && s[0] == '0' && (s[1] == 'b' || s[1] == 'B') && len(s) > 2 && (s[2] == '0' || s[2] == '1') {
		base, j = 2, 2
	}
	k := j
	for k < len(s) && digitOK(s[k], base) {
		k++
	}
	if base == 10 && k+1 < len(s) && s[k] == '.' && '0' <= s[k+1] && s[k+1] <= '9' {
		k++
		for k < len(s) && '0' <= s[k] && s[k] <= '9' {
			k++
		}
		f, err := strconv.ParseFloat(s[:k], 64)
		return FloatVal(f), k, err
	}
	if k < len(s) && isIdentPart(s[k]) {
		return Value{}, 0, fmt.Errorf("%w: bad number %q", ErrSyntax, s[:k+1])
	}
	n, err := strconv.ParseUint(s[j:k], base, 64)
	if err != nil {
		return Value{}, 0, fmt.Errorf("%w: bad number %q", ErrSyntax, s[:k])
	}
	return IntVal(int64(n)), k, nil
}

func digitOK(c byte, base int) bool {
	switch base {
	case 2:
		return c == '0' || c == '1'
	case 16:
		return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
	}
	return '0' <= c && c <= '9'
}

// scope is what an expression sees: labels, assignment and functions.
type scope interface {
	// lookup returns a label's value. The scope notes when the value is
	// stale: from an earlier pass, the label not being defined yet in
	// this one.
	lookup(name string) (Value, error)
	assign(name string, v Value) error
	// call calls a function other than the calculator's own; ok is false
	// when there is none of that name.
	call(name string, args []Value) (v Value, ok bool, err error)
	// eval evaluates the text of an expression (for val()).
	eval(expr string) (Value, error)
}

type parser struct {
	toks []token
	pos  int
	sc   scope
}

// evalExpr evaluates expr in sc.
func evalExpr(sc scope, expr string) (Value, error) {
	toks, err := tokenize(expr)
	if err != nil {
		return Value{}, err
	}
	p := &parser{toks: toks, sc: sc}
	v, err := p.expr()
	if err == nil && p.peek().k != tEOF {
		err = fmt.Errorf("%w: unexpected %q", ErrSyntax, p.peek().s)
	}
	return v, err
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) isOp(s string) bool {
	t := p.peek()
	return t.k == tOp && t.s == s
}

func (p *parser) expect(s string) error {
	if !p.isOp(s) {
		return fmt.Errorf("%w: %q expected", ErrSyntax, s)
	}
	p.pos++
	return nil
}

// Binary operator levels, lowest first. The original keeps a precedence
// per operator (reverse-engineered, #8) whose values are not known; these
// follow the usual order with comparisons binding tighter than AND/OR
// (inferred).
var levels = [][]string{
	{"OR"},
	{"XOR"},
	{"AND"},
	{"=", "!=", "<", ">", "<=", ">="},
	{"<<", ">>"},
	{"+", "-"},
	{"*", "/", "MOD"},
	{"^"},
}

func (p *parser) expr() (Value, error) {
	// name := expr (right-associative, lowest).
	if p.peek().k == tIdent && p.toks[p.pos+1].k == tOp && p.toks[p.pos+1].s == ":=" {
		name := p.peek().s
		p.pos += 2
		v, err := p.expr()
		if err != nil {
			return v, err
		}
		return v, p.sc.assign(name, v)
	}
	return p.binary(0)
}

func (p *parser) binary(level int) (Value, error) {
	if level == len(levels) {
		return p.unary()
	}
	a, err := p.binary(level + 1)
	if err != nil {
		return a, err
	}
	for {
		t := p.peek()
		found := false
		for _, op := range levels[level] {
			if t.k == tOp && t.s == op {
				found = true
			}
		}
		if !found {
			return a, nil
		}
		p.pos++
		next := level + 1
		if t.s == "^" {
			next = level // right-associative
		}
		if logicalOps[t.s] {
			v, err := p.logical(t.s, a, next)
			if err != nil {
				return v, err
			}
			a = v
			continue
		}
		b, err := p.binary(next)
		if err != nil {
			return b, err
		}
		if a, err = binop(t.s, a, b); err != nil {
			return a, err
		}
	}
}

// logicalOps are the operators whose operands may be written as a string
// holding an expression; see (*parser).logical.
var logicalOps = map[string]bool{"OR": true, "AND": true, "XOR": true}

// logical applies OR, AND or XOR. An operand may be a string holding an
// expression, which is calculated here: the sources quote the right-hand
// side — '(cVEConfigVersion MOD 1000)<167' — so that it is reached only
// when the left-hand side has not already settled the result, letting it
// name labels that a branch not taken need not define (inferred from how
// the sources use the operators, which are reverse-engineered, #8).
func (p *parser) logical(op string, a Value, next int) (Value, error) {
	a, err := p.evalStr(a)
	if err != nil {
		return a, err
	}
	b, err := p.binary(next)
	if err != nil {
		return b, err
	}
	if b.K == Str && a.K == Bool {
		if x := a.I != 0; op == "OR" && x || op == "AND" && !x {
			return BoolVal(x), nil
		}
	}
	if b, err = p.evalStr(b); err != nil {
		return b, err
	}
	return binop(op, a, b)
}

// evalStr calculates the text of a string operand as an expression.
func (p *parser) evalStr(v Value) (Value, error) {
	if v.K != Str {
		return v, nil
	}
	return p.sc.eval(v.S)
}

func (p *parser) unary() (Value, error) {
	switch {
	case p.isOp("-"):
		p.pos++
		v, err := p.unary()
		if err != nil {
			return v, err
		}
		switch v.K {
		case Int:
			return IntVal(-v.I), nil
		case Float:
			return FloatVal(-v.F), nil
		}
		return v, fmt.Errorf("%w: -%s", ErrType, v.kindName())
	case p.isOp("!"):
		p.pos++
		v, err := p.unary()
		if err != nil {
			return v, err
		}
		switch v.K {
		case Bool:
			return BoolVal(v.I == 0), nil
		case Int: // inferred: bitwise not
			return IntVal(^v.I), nil
		}
		return v, fmt.Errorf("%w: !%s", ErrType, v.kindName())
	}
	return p.index()
}

// index handles s@i: element i of a string (1-based, like copy) or of a
// list (0-based). The operator exists (reverse-engineered, #8); its base
// is inferred.
func (p *parser) index() (Value, error) {
	v, err := p.primary()
	for err == nil && p.isOp("@") {
		p.pos++
		var i Value
		if i, err = p.primary(); err != nil {
			return i, err
		}
		var n int64
		if n, err = i.AsInt(); err != nil {
			return i, err
		}
		switch {
		case v.K == Str && n >= 1 && n <= int64(len(v.S)):
			v = StrVal(v.S[n-1 : n])
		case v.K == List && n >= 0 && n < int64(len(v.L)):
			v = v.L[n]
		case v.K == Str || v.K == List:
			return v, fmt.Errorf("%w: index %d out of range", ErrType, n)
		default:
			return v, fmt.Errorf("%w: %s@", ErrType, v.kindName())
		}
	}
	return v, err
}

func (p *parser) primary() (Value, error) {
	t := p.peek()
	switch t.k {
	case tNum:
		p.pos++
		return t.v, nil
	case tStr:
		p.pos++
		return StrVal(t.s), nil
	case tIdent:
		p.pos++
		if p.isOp("(") {
			return p.call(t.s)
		}
		switch strings.ToLower(t.s) {
		case "true":
			return BoolVal(true), nil
		case "false":
			return BoolVal(false), nil
		case "pi":
			return FloatVal(math.Pi), nil
		}
		return p.sc.lookup(t.s)
	case tOp:
		switch t.s {
		case "(":
			p.pos++
			v, err := p.expr()
			if err != nil {
				return v, err
			}
			return v, p.expect(")")
		case "[":
			p.pos++
			l := ListVal()
			if p.isOp("]") {
				p.pos++
				return l, nil
			}
			for {
				v, err := p.expr()
				if err != nil {
					return v, err
				}
				l.L = append(l.L, v)
				if p.isOp("]") {
					p.pos++
					return l, nil
				}
				if err := p.expect(","); err != nil {
					return l, err
				}
			}
		}
	}
	if t.k == tEOF {
		return Value{}, fmt.Errorf("%w: expression ends early", ErrSyntax)
	}
	return Value{}, fmt.Errorf("%w: unexpected %q", ErrSyntax, t.s)
}

func (p *parser) call(name string) (Value, error) {
	p.pos++ // (
	var args []Value
	var argErr error
	if !p.isOp(")") {
		for {
			v, err := p.expr()
			if err != nil {
				// known() asks whether its argument can be calculated.
				if !errors.Is(err, ErrUnknown) || !strings.EqualFold(name, "known") {
					return v, err
				}
				argErr = err
				p.skipArg()
			}
			args = append(args, v)
			if p.isOp(")") {
				break
			}
			if err := p.expect(","); err != nil {
				return v, err
			}
		}
	}
	p.pos++ // )
	if strings.EqualFold(name, "known") {
		// The sources name the label as a string — Known('cVEConfigVersion')
		// — so what is asked is whether that expression can be calculated
		// (inferred; nearly every call in the sources quotes its argument).
		if argErr == nil && len(args) == 1 && args[0].K == Str {
			if _, err := p.sc.eval(args[0].S); err != nil {
				if !errors.Is(err, ErrUnknown) {
					return Value{}, err
				}
				argErr = err
			}
		}
		return BoolVal(argErr == nil), nil
	}
	if v, ok, err := p.sc.call(name, args); ok {
		return v, err
	}
	f, ok := builtins[strings.ToLower(name)]
	if !ok {
		return Value{}, fmt.Errorf("%w: no function %s", ErrUnknown, name)
	}
	return f(p, args)
}

// skipArg skips the rest of a function argument after an error.
func (p *parser) skipArg() {
	depth := 0
	for t := p.peek(); t.k != tEOF; t = p.peek() {
		if t.k == tOp {
			switch t.s {
			case "(", "[":
				depth++
			case ")", "]":
				if depth == 0 {
					return
				}
				depth--
			case ",":
				if depth == 0 {
					return
				}
			}
		}
		p.pos++
	}
}

func binop(op string, a, b Value) (Value, error) {
	bad := func() (Value, error) {
		return Value{}, fmt.Errorf("%w: %s %s %s", ErrType, a.kindName(), op, b.kindName())
	}
	switch op {
	case "=":
		return BoolVal(a.Equal(b)), nil
	case "!=":
		return BoolVal(!a.Equal(b)), nil
	case "<", ">", "<=", ">=":
		var c int
		switch {
		case a.K == Str && b.K == Str:
			c = strings.Compare(a.S, b.S)
		case a.K == Int && b.K == Int:
			c = cmpInt(a.I, b.I)
		case a.isNum() && b.isNum():
			c = cmpFloat(a.float(), b.float())
		default:
			return bad()
		}
		return BoolVal(map[string]bool{"<": c < 0, ">": c > 0, "<=": c <= 0, ">=": c >= 0}[op]), nil
	case "+":
		switch {
		case a.K == Str && b.K == Str:
			return StrVal(a.S + b.S), nil
		case a.K == List && b.K == List:
			return ListVal(append(append([]Value(nil), a.L...), b.L...)...), nil
		case a.K == List:
			return ListVal(append(append([]Value(nil), a.L...), b)...), nil
		}
	case "OR", "AND", "XOR":
		if a.K == Bool && b.K == Bool {
			x, y := a.I != 0, b.I != 0
			return BoolVal(map[string]bool{"OR": x || y, "AND": x && y, "XOR": x != y}[op]), nil
		}
		if a.K == Int && b.K == Int {
			return IntVal(map[string]int64{"OR": a.I | b.I, "AND": a.I & b.I, "XOR": a.I ^ b.I}[op]), nil
		}
		return bad()
	}
	if !a.isNum() || !b.isNum() {
		return bad()
	}
	if a.K == Int && b.K == Int {
		x, y := a.I, b.I
		switch op {
		case "+":
			return IntVal(x + y), nil
		case "-":
			return IntVal(x - y), nil
		case "*":
			return IntVal(x * y), nil
		case "/", "MOD":
			if y == 0 {
				return Value{}, ErrDivZero
			}
			if op == "/" {
				return IntVal(x / y), nil // truncates (reverse-engineered, #8)
			}
			return IntVal(x % y), nil
		case "<<":
			return IntVal(x << uint(y&63)), nil
		case ">>":
			return IntVal(x >> uint(y&63)), nil
		case "^":
			if y < 0 {
				return FloatVal(math.Pow(float64(x), float64(y))), nil
			}
			r := int64(1)
			for range y {
				r *= x
			}
			return IntVal(r), nil
		}
		return bad()
	}
	x, y := a.float(), b.float()
	switch op {
	case "+":
		return FloatVal(x + y), nil
	case "-":
		return FloatVal(x - y), nil
	case "*":
		return FloatVal(x * y), nil
	case "/":
		if y == 0 {
			return Value{}, ErrDivZero
		}
		return FloatVal(x / y), nil
	case "^":
		return FloatVal(math.Pow(x, y)), nil
	}
	return bad()
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

type builtin func(p *parser, args []Value) (Value, error)

// builtins are the calculator's functions (names reverse-engineered, #8).
// Their exact behaviour is inferred from the names, which follow Pascal's
// standard routines: positions in strings start at 1.
var builtins map[string]builtin

func init() {
	builtins = map[string]builtin{
		"int": num1(func(v Value) (Value, error) {
			i, err := v.AsInt()
			return IntVal(i), err
		}),
		"abs": num1(func(v Value) (Value, error) {
			if v.K == Float {
				return FloatVal(math.Abs(v.F)), nil
			}
			if v.I < 0 {
				return IntVal(-v.I), nil
			}
			return v, nil
		}),
		"sign": num1(func(v Value) (Value, error) {
			return IntVal(int64(cmpFloat(v.float(), 0))), nil
		}),
		"len": func(p *parser, a []Value) (Value, error) {
			if err := nargs(a, 1, 1); err != nil {
				return Value{}, err
			}
			switch a[0].K {
			case Str:
				return IntVal(int64(len(a[0].S))), nil
			case List:
				return IntVal(int64(len(a[0].L))), nil
			}
			return Value{}, fmt.Errorf("%w: len of %s", ErrType, a[0].kindName())
		},
		"copy": func(p *parser, a []Value) (Value, error) {
			if err := nargs(a, 2, 3); err != nil {
				return Value{}, err
			}
			n := int64(math.MaxInt32)
			if len(a) == 3 {
				var err error
				if n, err = a[2].AsInt(); err != nil {
					return Value{}, err
				}
			}
			return slice(a[0], a[1], n, false)
		},
		"del": func(p *parser, a []Value) (Value, error) {
			if err := nargs(a, 3, 3); err != nil {
				return Value{}, err
			}
			n, err := a[2].AsInt()
			if err != nil {
				return Value{}, err
			}
			return slice(a[0], a[1], n, true)
		},
		"ins": func(p *parser, a []Value) (Value, error) { // ins(sub, s, index)
			if err := nargs(a, 3, 3); err != nil {
				return Value{}, err
			}
			i, err := a[2].AsInt()
			if err != nil || a[0].K != Str || a[1].K != Str {
				return Value{}, fmt.Errorf("%w: ins(string, string, index)", ErrType)
			}
			s := a[1].S
			i = min(max(i, 1), int64(len(s))+1)
			return StrVal(s[:i-1] + a[0].S + s[i-1:]), nil
		},
		"pos": func(p *parser, a []Value) (Value, error) { // pos(sub, s) or pos(element, list)
			if err := nargs(a, 2, 2); err != nil {
				return Value{}, err
			}
			// In a list pos counts from 0 and is -1 when the element is
			// not there; in a string it counts from 1 and is 0 (inferred
			// from the comparisons the sources make, <0 against a list
			// and !=0 against a string).
			if a[1].K == List {
				for i, v := range a[1].L {
					if v.Equal(a[0]) {
						return IntVal(int64(i)), nil
					}
				}
				return IntVal(-1), nil
			}
			if a[0].K != Str || a[1].K != Str {
				return Value{}, fmt.Errorf("%w: pos(string, string)", ErrType)
			}
			return IntVal(int64(strings.Index(a[1].S, a[0].S) + 1)), nil
		},
		"val": func(p *parser, a []Value) (Value, error) {
			if err := nargs(a, 1, 1); err != nil {
				return Value{}, err
			}
			if a[0].K != Str {
				return a[0], nil
			}
			return p.sc.eval(a[0].S)
		},
		"ord": func(p *parser, a []Value) (Value, error) {
			if err := nargs(a, 1, 1); err != nil {
				return Value{}, err
			}
			switch {
			case a[0].K == Str && a[0].S != "":
				return IntVal(int64(a[0].S[0])), nil
			case a[0].K == Bool || a[0].K == Int:
				return IntVal(a[0].I), nil
			}
			return Value{}, fmt.Errorf("%w: ord of %s", ErrType, a[0].kindName())
		},
		"str": func(p *parser, a []Value) (Value, error) {
			if err := nargs(a, 1, 1); err != nil {
				return Value{}, err
			}
			return StrVal(a[0].String()), nil
		},
		"hex": func(p *parser, a []Value) (Value, error) { // hex(x[, digits])
			if err := nargs(a, 1, 2); err != nil {
				return Value{}, err
			}
			x, err := a[0].AsInt()
			if err != nil {
				return Value{}, err
			}
			w := int64(1)
			if len(a) == 2 {
				if w, err = a[1].AsInt(); err != nil {
					return Value{}, err
				}
			}
			return StrVal(fmt.Sprintf("%0*X", w, uint64(x))), nil
		},
		"err": func(p *parser, a []Value) (Value, error) {
			// Err(condition, message) raises message only when the
			// condition holds, and is 0 otherwise so that it can be added
			// into an expression, which is how the sources use it
			// (reverse-engineered, #8).
			if len(a) == 2 {
				ok, err := a[0].AsBool()
				if err != nil {
					return Value{}, err
				}
				if !ok {
					return IntVal(0), nil
				}
				return Value{}, &UserError{Msg: a[1].String()}
			}
			msg := "error"
			if len(a) > 0 {
				msg = a[0].String()
			}
			return Value{}, &UserError{Msg: msg}
		},
		"round": real1(math.RoundToEven, true), // Delphi rounds halves to even (inferred)
		"trunc": real1(math.Trunc, true),
		"sqrt":  real1(math.Sqrt, false),
		"sin":   real1(math.Sin, false),
		"cos":   real1(math.Cos, false),
		"tan":   real1(math.Tan, false),
		"asin":  real1(math.Asin, false),
		"acos":  real1(math.Acos, false),
		"atan":  real1(math.Atan, false),
	}
}

// UserError is an error raised by the source (err(), .ERROR, .FERROR).
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

func nargs(a []Value, lo, hi int) error {
	if len(a) < lo || len(a) > hi {
		return fmt.Errorf("%w: %d arguments", ErrSyntax, len(a))
	}
	return nil
}

func num1(f func(Value) (Value, error)) builtin {
	return func(p *parser, a []Value) (Value, error) {
		if err := nargs(a, 1, 1); err != nil {
			return Value{}, err
		}
		if !a[0].isNum() && a[0].K != Bool {
			return Value{}, fmt.Errorf("%w: number expected", ErrType)
		}
		return f(a[0])
	}
}

func real1(f func(float64) float64, toInt bool) builtin {
	return num1(func(v Value) (Value, error) {
		r := f(v.float())
		if toInt {
			return IntVal(int64(r)), nil
		}
		return FloatVal(r), nil
	})
}

// slice returns copy(v, at, n), or with del set the rest of v without
// those elements. at counts from 1.
func slice(v, at Value, n int64, del bool) (Value, error) {
	i, err := at.AsInt()
	if err != nil {
		return Value{}, err
	}
	var size int64
	switch v.K {
	case Str:
		size = int64(len(v.S))
	case List:
		size = int64(len(v.L))
	default:
		return Value{}, fmt.Errorf("%w: copy/del of %s", ErrType, v.kindName())
	}
	lo := min(max(i, 1), size+1) - 1
	hi := min(lo+max(n, 0), size)
	if v.K == Str {
		if del {
			return StrVal(v.S[:lo] + v.S[hi:]), nil
		}
		return StrVal(v.S[lo:hi]), nil
	}
	if del {
		return ListVal(append(append([]Value(nil), v.L[:lo]...), v.L[hi:]...)...), nil
	}
	return ListVal(append([]Value(nil), v.L[lo:hi]...)...), nil
}
