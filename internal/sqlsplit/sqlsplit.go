// Package sqlsplit splits SQL scripts into statements the way each
// engine's command-line client does, so that a script can be run one
// statement at a time and the editor can map the cursor to a statement.
//
// The scanner understands, depending on the dialect:
//
//   - '…' strings with ” escapes (MySQL also \-escapes; PostgreSQL only in
//     E'…' strings)
//   - "…" quoted identifiers (MySQL: strings with \-escapes), `…`
//     identifiers on MySQL and SQLite, […] identifiers on SQLite
//   - -- line comments (MySQL requires whitespace after them), # comments
//     on MySQL, /* … */ block comments (nested on PostgreSQL)
//   - PostgreSQL dollar quoting: $$…$$ and $tag$…$tag$
//   - the client-side MySQL DELIMITER directive (on its own line)
//   - BEGIN … END bodies of SQLite CREATE TRIGGER and of PostgreSQL
//     BEGIN ATOMIC function bodies
//
// Offsets are byte offsets into the script. Unterminated strings, comments
// and quotes run to the end of the script; the scanner never fails.
package sqlsplit

import (
	"strings"

	"dbedit/internal/driver"
)

// Statement is one statement of a script.
type Statement struct {
	// Text is the statement without leading whitespace/comments and
	// without its delimiter: script[Start:End].
	Text string `json:"text"`
	// Start is the byte offset of the first character of the statement.
	Start int `json:"start"`
	// End is the byte offset just after its last character (before the
	// delimiter and any whitespace preceding it).
	End int `json:"end"`
	// DelimEnd is the byte offset just after the delimiter that ended the
	// statement (End when it ended at the end of the script).
	DelimEnd int `json:"delimEnd"`
}

// dialect captures the lexical rules that differ between engines.
type dialect struct {
	backslashStrings bool // '…' and "…" use \ escapes (MySQL)
	doubleIsString   bool // "…" is a string (MySQL) rather than an identifier
	backticks        bool // `ident`
	brackets         bool // [ident] (SQLite)
	hashComments     bool // # comment (MySQL)
	dashNeedsSpace   bool // "-- " must be followed by whitespace (MySQL)
	nestedComments   bool // /* /* */ */ (PostgreSQL)
	dollarQuotes     bool // $tag$…$tag$ (PostgreSQL)
	eStrings         bool // E'…' (PostgreSQL)
	delimiter        bool // DELIMITER directive (MySQL client)
	triggerBodies    bool // CREATE TRIGGER … BEGIN … END (SQLite)
	atomicBodies     bool // BEGIN ATOMIC … END (PostgreSQL)
}

func dialectFor(kind driver.Kind) dialect {
	switch kind {
	case driver.Postgres:
		return dialect{nestedComments: true, dollarQuotes: true, eStrings: true, atomicBodies: true}
	case driver.MySQL, driver.MariaDB:
		return dialect{backslashStrings: true, doubleIsString: true, backticks: true, hashComments: true, dashNeedsSpace: true, delimiter: true}
	default: // SQLite and unknown kinds: SQLite's rules are close to ANSI.
		return dialect{backticks: true, brackets: true, triggerBodies: true}
	}
}

// scanner holds the state of one Split call.
type scanner struct {
	d     dialect
	s     string
	i     int
	delim string
	out   []Statement

	// current statement
	start     int // -1 until the first content token
	end       int // end of the last content token
	words     int // words seen in the current statement (up to wordLimit)
	first     [wordLimit]string
	depth     int  // BEGIN/CASE … END nesting inside a body
	bodyKind  int  // bodyNone, bodyTrigger, bodyAtomic
	prevBegin bool // the previous word was BEGIN (for BEGIN ATOMIC)
	lineStart bool // only blanks since the last newline
	wordEnd   int  // offset just after the last word or number token

	onWord func(upper string) // optional hook for every word (Words)
}

const wordLimit = 6

const (
	bodyNone = iota
	bodyTrigger
	bodyAtomic
)

// Split splits script into statements using the rules of kind. Empty
// statements (only whitespace, comments or delimiters) are dropped.
func Split(kind driver.Kind, script string) []Statement {
	sc := &scanner{d: dialectFor(kind), s: script, delim: ";", start: -1, lineStart: true, wordEnd: -1}
	sc.run()
	if sc.out == nil {
		return []Statement{}
	}
	return sc.out
}

// Words returns the bare words (keywords and unquoted identifiers,
// upper-cased) of a statement, skipping strings, quoted identifiers and
// comments. At most limit words are returned (limit <= 0: all).
func Words(kind driver.Kind, text string, limit int) []string {
	out := []string{}
	sc := &scanner{d: dialectFor(kind), s: text, delim: ";", start: -1, lineStart: true, wordEnd: -1}
	sc.onWord = func(w string) {
		if limit <= 0 || len(out) < limit {
			out = append(out, w)
		}
	}
	sc.run()
	return out
}

// StatementAt returns the statement the cursor (a byte offset) belongs to:
// the statement containing it, including its delimiter; otherwise the
// closest statement before it (the cursor is in the blank space after a
// statement), otherwise the first statement after it. ok is false when the
// script has no statements.
func StatementAt(kind driver.Kind, script string, cursor int) (st Statement, ok bool) {
	stmts := Split(kind, script)
	if len(stmts) == 0 {
		return Statement{}, false
	}
	cursor = max(0, min(cursor, len(script)))
	for i, s := range stmts {
		if cursor >= s.Start && cursor <= s.DelimEnd {
			return s, true
		}
		if cursor < s.Start {
			if i == 0 {
				return s, true
			}
			return stmts[i-1], true
		}
	}
	return stmts[len(stmts)-1], true
}

func (sc *scanner) run() {
	for sc.i < len(sc.s) {
		if sc.d.delimiter && sc.lineStart && sc.directive() {
			continue
		}
		c := sc.s[sc.i]
		switch {
		case c == '\n':
			sc.i++
			sc.lineStart = true
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			sc.i++
			continue
		}
		sc.lineStart = false
		if sc.depth == 0 && strings.HasPrefix(sc.s[sc.i:], sc.delim) {
			sc.i += len(sc.delim)
			sc.flush(sc.i)
			continue
		}
		sc.token()
	}
	sc.flush(len(sc.s))
}

// flush ends the current statement; delimEnd is the offset after its
// delimiter.
func (sc *scanner) flush(delimEnd int) {
	if sc.start >= 0 {
		sc.out = append(sc.out, Statement{Text: sc.s[sc.start:sc.end], Start: sc.start, End: sc.end, DelimEnd: delimEnd})
	}
	sc.start, sc.end, sc.words, sc.depth, sc.bodyKind = -1, 0, 0, 0, bodyNone
	sc.prevBegin = false
}

// content marks s[from:to] as part of the current statement.
func (sc *scanner) content(from, to int) {
	if sc.start < 0 {
		sc.start = from
	}
	sc.end = to
}

// token consumes one token at sc.i (not whitespace).
func (sc *scanner) token() {
	s, i := sc.s, sc.i
	c := s[i]
	next := byte(0)
	if i+1 < len(s) {
		next = s[i+1]
	}
	// afterWord: the token directly follows a word or number (no blank).
	afterWord := sc.wordEnd == i
	switch {
	case c == '-' && next == '-' && (!sc.d.dashNeedsSpace || i+2 >= len(s) || isSpaceOrCtl(s[i+2])):
		sc.i = lineEnd(s, i)
		sc.lineStart = true
		return
	case c == '#' && sc.d.hashComments:
		sc.i = lineEnd(s, i)
		sc.lineStart = true
		return
	case c == '/' && next == '*':
		sc.i = sc.blockCommentEnd(i)
		// MySQL executable comments (/*! … */) and optimizer hints
		// (/*+ … */) are code.
		if sc.d.hashComments && i+2 < len(s) && (s[i+2] == '!' || s[i+2] == '+') {
			sc.content(i, sc.i)
		}
		return
	case c == '\'':
		escapes := sc.d.backslashStrings
		if sc.d.eStrings && afterWord && (s[i-1] == 'E' || s[i-1] == 'e') && (i < 2 || !isIdent(s[i-2])) {
			escapes = true
		}
		sc.i = quoteEnd(s, i, '\'', escapes)
	case c == '"':
		sc.i = quoteEnd(s, i, '"', sc.d.backslashStrings && sc.d.doubleIsString)
	case c == '`' && sc.d.backticks:
		sc.i = quoteEnd(s, i, '`', false)
	case c == '[' && sc.d.brackets:
		if j := strings.IndexByte(s[i+1:], ']'); j >= 0 {
			sc.i = i + j + 2
		} else {
			sc.i = len(s)
		}
	case c == '$' && sc.d.dollarQuotes && !afterWord:
		if end, ok := dollarEnd(s, i); ok {
			sc.i = end
		} else {
			sc.i = i + 1
		}
	case isIdentStart(c):
		j := i + 1
		for j < len(s) && isIdent(s[j]) && !sc.delimAt(j) {
			j++
		}
		sc.i = j
		sc.content(i, j)
		sc.word(s[i:j])
		sc.wordEnd = j
		return
	case isDigit(c):
		j := i + 1
		for j < len(s) && (isIdent(s[j]) || s[j] == '.') && !sc.delimAt(j) {
			j++
		}
		sc.i = j
		sc.wordEnd = j // 1e'x' is not an E-string
	default:
		sc.i = i + 1
	}
	sc.content(i, sc.i)
}

// word tracks keywords for BEGIN … END bodies.
func (sc *scanner) word(w string) {
	up := strings.ToUpper(w)
	if sc.onWord != nil {
		sc.onWord(up)
	}
	if sc.words < wordLimit {
		sc.first[sc.words] = up
		sc.words++
		if sc.bodyKind == bodyNone {
			sc.bodyKind = sc.detectBody()
		}
	}
	switch sc.bodyKind {
	case bodyTrigger:
		switch up {
		case "BEGIN", "CASE":
			sc.depth++
		case "END":
			if sc.depth > 0 {
				sc.depth--
			}
		}
	case bodyAtomic:
		switch {
		case up == "ATOMIC" && sc.prevBegin:
			sc.depth++
		case up == "CASE" && sc.depth > 0:
			sc.depth++
		case up == "END" && sc.depth > 0:
			sc.depth--
		}
	}
	sc.prevBegin = up == "BEGIN"
}

// detectBody recognises CREATE [TEMP|TEMPORARY] TRIGGER (SQLite) and
// CREATE [OR REPLACE] FUNCTION|PROCEDURE (PostgreSQL) from the first words.
func (sc *scanner) detectBody() int {
	w := sc.first[:sc.words]
	if len(w) < 2 || w[0] != "CREATE" {
		return bodyNone
	}
	rest := w[1:]
	if sc.d.triggerBodies {
		if rest[0] == "TEMP" || rest[0] == "TEMPORARY" {
			rest = rest[1:]
		}
		if len(rest) > 0 && rest[0] == "TRIGGER" {
			return bodyTrigger
		}
	}
	if sc.d.atomicBodies {
		if len(rest) >= 2 && rest[0] == "OR" && rest[1] == "REPLACE" {
			rest = rest[2:]
		}
		if len(rest) > 0 && (rest[0] == "FUNCTION" || rest[0] == "PROCEDURE") {
			return bodyAtomic
		}
	}
	return bodyNone
}

// delimAt reports whether a custom (MySQL DELIMITER) delimiter starts at
// j. Like the mysql client, it also ends a statement in the middle of a
// word ("END$$").
func (sc *scanner) delimAt(j int) bool {
	return sc.delim != ";" && sc.depth == 0 && strings.HasPrefix(sc.s[j:], sc.delim)
}

// directive handles a MySQL client DELIMITER line at sc.i (which is at the
// start of a line, possibly after blanks). It reports whether one was
// consumed.
func (sc *scanner) directive() bool {
	s, i := sc.s, sc.i
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	const kw = "DELIMITER"
	if len(s)-i <= len(kw) || !strings.EqualFold(s[i:i+len(kw)], kw) || (s[i+len(kw)] != ' ' && s[i+len(kw)] != '\t') {
		return false
	}
	end := lineEnd(s, i)
	arg := strings.Fields(s[i+len(kw) : end])
	if len(arg) == 0 {
		return false
	}
	// A pending statement ends where the directive begins.
	sc.flush(i)
	sc.delim = arg[0]
	sc.i = end
	return true
}

// blockCommentEnd returns the offset after the comment starting at i.
func (sc *scanner) blockCommentEnd(i int) int {
	s := sc.s
	depth := 0
	for j := i; j+1 < len(s); {
		switch {
		case s[j] == '/' && s[j+1] == '*':
			if depth == 0 || sc.d.nestedComments {
				depth++
			}
			j += 2
		case s[j] == '*' && s[j+1] == '/':
			depth--
			j += 2
			if depth == 0 {
				return j
			}
		default:
			j++
		}
	}
	return len(s)
}

// quoteEnd returns the offset after the quoted token starting at i. The
// quote is escaped by doubling it, and by a backslash when escapes is set.
func quoteEnd(s string, i int, q byte, escapes bool) int {
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if escapes {
				j++
			}
		case q:
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(s)
}

// dollarEnd returns the offset after a dollar-quoted string starting at i,
// or ok=false when s[i:] does not start a dollar quote ($1 parameters, a
// lone $).
func dollarEnd(s string, i int) (int, bool) {
	j := i + 1
	if j < len(s) && isIdentStart(s[j]) {
		for j < len(s) && isIdent(s[j]) && s[j] != '$' {
			j++
		}
	}
	if j >= len(s) || s[j] != '$' {
		return 0, false
	}
	tag := s[i : j+1]
	if k := strings.Index(s[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag), true
	}
	return len(s), true
}

// lineEnd returns the offset after the newline ending the line at i (or
// the end of s).
func lineEnd(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j + 1
	}
	return len(s)
}

func isSpaceOrCtl(c byte) bool { return c <= ' ' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }

// isIdentStart: letters, underscore and any non-ASCII byte (UTF-8
// identifiers).
func isIdentStart(c byte) bool {
	return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') || c >= 0x80
}

func isIdent(c byte) bool { return isIdentStart(c) || isDigit(c) || c == '$' }
