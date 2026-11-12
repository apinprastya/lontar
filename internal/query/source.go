package query

import (
	"strings"

	dbdriver "lontar/internal/driver"
)

// This file recognises the one query shape whose result can be edited like
// a table: a SELECT that reads one table, with no joins, grouping, DISTINCT
// or set operations. The parse is deliberately conservative: anything it
// does not understand makes the result read-only. Nothing here is ever
// executed; the change set built from an editable result is validated
// against introspected metadata like any other (see package edits).

// SelectItem is one entry of a SELECT list.
type SelectItem struct {
	// Star is a * or table.* entry.
	Star bool
	// Column is the column a plain column reference reads ("" for an
	// expression). Quoted is true when it was written as a quoted
	// identifier (so its case is exact).
	Column string
	Quoted bool
}

// SelectSource is the table a single-table SELECT reads.
type SelectSource struct {
	// Schema is "" when the table name is not qualified.
	Schema       string
	SchemaQuoted bool
	Table        string
	TableQuoted  bool
	Items        []SelectItem
}

// Reasons a query result is not editable (shown to the user).
const (
	ReasonNotSelect   = "Only results of a SELECT from a single table can be edited."
	ReasonJoin        = "Results that combine several tables (joins, subqueries in FROM, set operations) are read-only."
	ReasonAggregate   = "Grouped or DISTINCT results are read-only."
	ReasonUnsupported = "Lontar could not tell which table this result comes from, so it is read-only."
)

type tokKind int

const (
	tokWord   tokKind = iota // bare word (keyword or identifier)
	tokQuoted                // quoted identifier
	tokString                // string literal
	tokNumber
	tokPunct // one character: ( ) , . * ; and operators
)

type token struct {
	kind tokKind
	text string // unquoted text for tokQuoted; raw otherwise
	up   string // upper-cased text of a tokWord
}

// tokenize splits a statement into tokens, skipping comments and spaces.
// ok is false for text it cannot read (an unterminated quote).
func tokenize(kind dbdriver.Kind, s string) (out []token, ok bool) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c <= ' ':
			i++
		case c == '-' && i+1 < len(s) && s[i+1] == '-', c == '#' && kind == dbdriver.MySQL:
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, false
			}
			i += end + 4
		case c == '\'':
			j, ok := quoted(s, i, '\'', kind == dbdriver.MySQL)
			if !ok {
				return nil, false
			}
			out = append(out, token{kind: tokString, text: s[i:j]})
			i = j
		case c == '"' || c == '`' || c == '[':
			if c == '"' && kind == dbdriver.MySQL {
				// MySQL's default: double quotes make a string.
				j, ok := quoted(s, i, '"', true)
				if !ok {
					return nil, false
				}
				out = append(out, token{kind: tokString, text: s[i:j]})
				i = j
				continue
			}
			if (c == '`' && kind == dbdriver.Postgres) || (c == '[' && kind != dbdriver.SQLite) {
				return nil, false
			}
			closer := c
			if c == '[' {
				closer = ']'
			}
			j, ok := quoted(s, i, closer, false)
			if !ok {
				return nil, false
			}
			name := s[i+1 : j-1]
			if closer != ']' {
				name = strings.ReplaceAll(name, string([]byte{closer, closer}), string(closer))
			}
			out = append(out, token{kind: tokQuoted, text: name})
			i = j
		case c == '$' && kind == dbdriver.Postgres && i+1 < len(s) && (s[i+1] == '$' || isWordStart(s[i+1])):
			// Dollar-quoted string ($$…$$ or $tag$…$tag$); $1 is a parameter.
			end := strings.IndexByte(s[i+1:], '$')
			if end < 0 {
				return nil, false
			}
			tag := s[i : i+end+2]
			end2 := strings.Index(s[i+len(tag):], tag)
			if end2 < 0 {
				return nil, false
			}
			j := i + len(tag) + end2 + len(tag)
			out = append(out, token{kind: tokString, text: s[i:j]})
			i = j
		case isWordStart(c):
			j := i
			for j < len(s) && isWordChar(s[j]) {
				j++
			}
			w := s[i:j]
			// E'…' / X'…' / N'…' prefixed strings.
			if j < len(s) && s[j] == '\'' && len(w) == 1 {
				k, ok := quoted(s, j, '\'', true)
				if !ok {
					return nil, false
				}
				out = append(out, token{kind: tokString, text: s[i:k]})
				i = k
				continue
			}
			out = append(out, token{kind: tokWord, text: w, up: strings.ToUpper(w)})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && (isWordChar(s[j]) || s[j] == '.') {
				j++
			}
			out = append(out, token{kind: tokNumber, text: s[i:j]})
			i = j
		default:
			out = append(out, token{kind: tokPunct, text: s[i : i+1]})
			i++
		}
	}
	return out, true
}

// quoted returns the index after the quote that closes the one at s[i].
// A doubled closer is an escaped closer; with backslash, \x escapes too.
func quoted(s string, i int, closer byte, backslash bool) (int, bool) {
	for j := i + 1; j < len(s); j++ {
		switch {
		case backslash && s[j] == '\\':
			j++
		case s[j] == closer:
			if closer != ']' && j+1 < len(s) && s[j+1] == closer {
				j++
				continue
			}
			return j + 1, true
		}
	}
	return 0, false
}

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isWordChar(c byte) bool { return isWordStart(c) || (c >= '0' && c <= '9') || c == '$' }

// Words that end the table reference of the FROM clause.
var clauseAfterFrom = map[string]bool{"WHERE": true, "ORDER": true, "LIMIT": true, "OFFSET": true, "FETCH": true, "FOR": true}

// Words that make a result read-only wherever they appear at the top level.
var multiTableWords = map[string]bool{
	"JOIN": true, "UNION": true, "INTERSECT": true, "EXCEPT": true, "MINUS": true,
	"NATURAL": true, "CROSS": true, "LATERAL": true, "INTO": true,
}

var groupingWords = map[string]bool{"GROUP": true, "HAVING": true, "WINDOW": true, "DISTINCT": true, "DISTINCTROW": true}

// ParseSelectSource recognises a single-table SELECT. On failure it returns
// nil and the reason the result is read-only.
func ParseSelectSource(kind dbdriver.Kind, text string) (*SelectSource, string) {
	toks, ok := tokenize(kind, text)
	if !ok || len(toks) == 0 {
		return nil, ReasonUnsupported
	}
	// A trailing semicolon ends the statement.
	for len(toks) > 0 && toks[len(toks)-1].kind == tokPunct && toks[len(toks)-1].text == ";" {
		toks = toks[:len(toks)-1]
	}
	if toks[0].kind != tokWord || toks[0].up != "SELECT" {
		return nil, ReasonNotSelect
	}

	// Scan the top level: find FROM and reject what can't be edited.
	from := -1
	depth := 0
	for i, t := range toks {
		switch {
		case t.kind == tokPunct && t.text == "(":
			depth++
		case t.kind == tokPunct && t.text == ")":
			depth--
			if depth < 0 {
				return nil, ReasonUnsupported
			}
		case t.kind == tokPunct && t.text == ";" && depth == 0:
			return nil, ReasonUnsupported // several statements
		case t.kind == tokWord && depth == 0:
			if multiTableWords[t.up] {
				return nil, ReasonJoin
			}
			if groupingWords[t.up] {
				return nil, ReasonAggregate
			}
			if t.up == "FROM" && from < 0 {
				from = i
			}
		}
	}
	if depth != 0 || from < 0 {
		return nil, ReasonUnsupported
	}

	src := &SelectSource{}
	if !parseTableRef(toks[from+1:], src) {
		return nil, ReasonJoin
	}

	// SELECT [ALL] list.
	list := toks[1:from]
	if len(list) > 0 && list[0].kind == tokWord && list[0].up == "ALL" {
		list = list[1:]
	}
	if len(list) == 0 {
		return nil, ReasonUnsupported
	}
	for _, item := range splitTop(list) {
		src.Items = append(src.Items, parseItem(item))
	}
	return src, ""
}

// parseTableRef reads `table` or `schema.table`, an optional alias and then
// the end or a clause keyword (WHERE, ORDER BY, …).
func parseTableRef(toks []token, src *SelectSource) bool {
	name := func(t token) bool { return t.kind == tokWord || t.kind == tokQuoted }
	if len(toks) == 0 || !name(toks[0]) || (toks[0].kind == tokWord && toks[0].up == "ONLY") {
		return false
	}
	n := 1
	if len(toks) >= 3 && toks[1].kind == tokPunct && toks[1].text == "." && name(toks[2]) {
		src.Schema, src.SchemaQuoted = toks[0].text, toks[0].kind == tokQuoted
		src.Table, src.TableQuoted = toks[2].text, toks[2].kind == tokQuoted
		n = 3
	} else {
		src.Table, src.TableQuoted = toks[0].text, toks[0].kind == tokQuoted
	}
	rest := toks[n:]
	// [AS] alias
	if len(rest) > 0 && rest[0].kind == tokWord && rest[0].up == "AS" {
		rest = rest[1:]
		if len(rest) == 0 || !name(rest[0]) {
			return false
		}
		rest = rest[1:]
	} else if len(rest) > 0 && (rest[0].kind == tokQuoted || (rest[0].kind == tokWord && !clauseAfterFrom[rest[0].up])) {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return true
	}
	return rest[0].kind == tokWord && clauseAfterFrom[rest[0].up]
}

// splitTop splits tokens at top-level commas.
func splitTop(toks []token) [][]token {
	var out [][]token
	depth, start := 0, 0
	for i, t := range toks {
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				out = append(out, toks[start:i])
				start = i + 1
			}
		}
	}
	return append(out, toks[start:])
}

// parseItem classifies one SELECT list entry: *, q.*, a plain column
// reference ([schema.][table.]column) with an optional alias, or an
// expression.
func parseItem(toks []token) SelectItem {
	isName := func(t token) bool { return t.kind == tokWord || t.kind == tokQuoted }
	isDot := func(t token) bool { return t.kind == tokPunct && t.text == "." }
	isStar := func(t token) bool { return t.kind == tokPunct && t.text == "*" }

	// Strip an alias: `expr AS name` or `column name`.
	body := toks
	if n := len(toks); n >= 3 && toks[n-2].kind == tokWord && toks[n-2].up == "AS" && isName(toks[n-1]) {
		body = toks[:n-2]
	} else if n >= 2 && isName(toks[n-1]) && isName(toks[n-2]) {
		body = toks[:n-1]
	}

	// name(.name){0,2} or (name.){0,2}*
	parts := []token{}
	for i, t := range body {
		if i%2 == 1 {
			if !isDot(t) {
				return SelectItem{}
			}
			continue
		}
		if !isName(t) && !(isStar(t) && i == len(body)-1) {
			return SelectItem{}
		}
		parts = append(parts, t)
	}
	if len(parts) == 0 || len(parts) > 3 || len(body)%2 == 0 {
		return SelectItem{}
	}
	last := parts[len(parts)-1]
	if isStar(last) {
		// Only a bare * or q.* (no alias allowed).
		if len(body) != len(toks) {
			return SelectItem{}
		}
		return SelectItem{Star: true}
	}
	// A bare keyword such as NULL, TRUE or CURRENT_DATE is an expression.
	if len(parts) == 1 && last.kind == tokWord && exprWords[last.up] {
		return SelectItem{}
	}
	return SelectItem{Column: last.text, Quoted: last.kind == tokQuoted}
}

// exprWords are bare words that are values, not column names.
var exprWords = map[string]bool{
	"NULL": true, "TRUE": true, "FALSE": true, "DEFAULT": true,
	"CURRENT_DATE": true, "CURRENT_TIME": true, "CURRENT_TIMESTAMP": true,
	"LOCALTIME": true, "LOCALTIMESTAMP": true, "CURRENT_USER": true, "SESSION_USER": true, "USER": true,
}
