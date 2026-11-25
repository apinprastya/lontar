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
//   - PostgreSQL COPY … FROM stdin; followed by its data block (psql and
//     pg_dump plain format), up to a line holding only \.
//   - psql meta-commands (\connect, \restrict, …) at the start of a
//     statement on PostgreSQL: the rest of the line, as a Meta statement
//
// There is one implementation: Scanner reads statements from a stream with
// a bounded buffer (a statement may be at most MaxStatement bytes; COPY
// data is streamed and not limited), and Split runs it over a string.
//
// Offsets are byte offsets into the script. Unterminated strings, comments
// and quotes run to the end of the script; splitting a string never fails.
package sqlsplit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"lontar/internal/driver"
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
	// Line is the 1-based line of Start.
	Line int `json:"line"`
	// CopyData is the data block of a PostgreSQL COPY … FROM stdin
	// statement (nil otherwise): the lines after the statement up to the
	// \. line, which is not part of it. When streaming, it must be read
	// before the next call to Scanner.Next (which skips what is left).
	// Reading it fails with ErrCopyUnterminated when the script ends
	// without the \. line.
	CopyData io.Reader `json:"-"`
	// Meta marks a psql meta-command line (PostgreSQL): Text is the line,
	// starting with a backslash. It is not SQL.
	Meta bool `json:"meta,omitempty"`
}

// MaxStatement is the largest statement (or comment, or quoted token)
// a Scanner reads: larger ones fail with ErrStatementTooLarge.
const MaxStatement = 64 << 20

// Errors of a Scanner.
var (
	ErrStatementTooLarge = errors.New("statement too large")
	ErrCopyUnterminated  = errors.New(`COPY data is not terminated by a line with \.`)
)

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
	copyData         bool // COPY … FROM stdin; data blocks (psql)
	metaCommands     bool // \command lines (psql)
}

func dialectFor(kind driver.Kind) dialect {
	switch kind {
	case driver.Postgres:
		return dialect{nestedComments: true, dollarQuotes: true, eStrings: true, atomicBodies: true, copyData: true, metaCommands: true}
	case driver.MySQL, driver.MariaDB:
		return dialect{backslashStrings: true, doubleIsString: true, backticks: true, hashComments: true, dashNeedsSpace: true, delimiter: true}
	default: // SQLite and unknown kinds: SQLite's rules are close to ANSI.
		return dialect{backticks: true, brackets: true, triggerBodies: true}
	}
}

// state is the part of a Scanner that one step changes. A step that runs
// into the end of the buffer before the end of the input is undone by
// restoring the state, and repeated after reading more, so the result
// never depends on how the input is chunked.
type state struct {
	i     int    // next byte (buffer offset)
	delim string // current delimiter

	// current statement
	start     int // -1 until the first content token
	end       int // end of the last content token
	words     int // words seen in the current statement (up to wordLimit)
	first     [wordLimit]string
	depth     int  // BEGIN/CASE … END nesting inside a body
	bodyKind  int  // bodyNone, bodyTrigger, bodyAtomic
	prevBegin bool // the previous word was BEGIN (for BEGIN ATOMIC)
	prevFrom  bool // the previous word was FROM (for COPY … FROM stdin)
	fromStdin bool // the statement is COPY … FROM STDIN
	lineStart bool // only blanks since the last newline
	wordEnd   int  // offset just after the last word or number token

	nl        int // newlines before linePos
	linePos   int // buffer offset up to which newlines are counted
	startLine int // line of start

	pending bool      // out holds a finished statement
	out     Statement // (offsets absolute)
	copyIn  bool      // out is a COPY … FROM stdin with a data block
	meta    bool      // the current statement is a meta-command
}

// Scanner reads the statements of a script from a stream.
type Scanner struct {
	state
	d     dialect
	r     io.Reader
	buf   []byte
	base  int // absolute offset of buf[0]
	eof   bool
	done  bool
	short bool  // the current step needs more input
	err   error // sticky error
	// readErr is a read error to report once the data read with it is used.
	readErr error
	max     int
	copy    *copyReader // the data block being read

	onWord func(upper string) // optional hook for every word (Words)
	// onParam is an optional hook for every :name placeholder (Params);
	// start and end are absolute offsets of the colon and after the name.
	onParam func(name string, start, end int)
}

const wordLimit = 6

const (
	bodyNone = iota
	bodyTrigger
	bodyAtomic
)

// readSize is the minimum read from the stream.
const readSize = 64 << 10

// NewScanner returns a Scanner reading the script of kind from r.
func NewScanner(kind driver.Kind, r io.Reader) *Scanner {
	return &Scanner{state: newState(), d: dialectFor(kind), r: r, max: MaxStatement}
}

func newState() state {
	return state{delim: ";", start: -1, lineStart: true, wordEnd: -1}
}

// stringScanner scans an in-memory script (no reads, no size limit).
func stringScanner(kind driver.Kind, script string) *Scanner {
	return &Scanner{state: newState(), d: dialectFor(kind), buf: []byte(script), eof: true}
}

// Offset returns how many bytes of the input the scanner has consumed.
func (sc *Scanner) Offset() int64 { return int64(sc.base + sc.i) }

// Next returns the next statement, or io.EOF after the last one. Empty
// statements (only whitespace, comments or delimiters) are skipped.
// Errors (reading, ErrStatementTooLarge, ErrCopyUnterminated) are final.
func (sc *Scanner) Next() (Statement, error) {
	if sc.copy != nil {
		if _, err := io.Copy(io.Discard, sc.copy); err != nil && sc.err == nil {
			sc.err = err
		}
		sc.copy = nil
	}
	for {
		if sc.err != nil {
			return Statement{}, sc.err
		}
		if sc.pending {
			st := sc.out
			if sc.copyIn {
				sc.copy = &copyReader{sc: sc, skipLine: true, atLineStart: true}
				st.CopyData = sc.copy
			}
			sc.pending, sc.copyIn, sc.out = false, false, Statement{}
			return st, nil
		}
		if sc.done {
			return Statement{}, io.EOF
		}
		if sc.eof {
			// Everything is buffered: a step never needs more input.
			sc.short = false
			sc.step()
			continue
		}
		saved := sc.state
		sc.short = false
		sc.step()
		if sc.short {
			sc.state = saved
			sc.err = sc.fill()
		}
	}
}

// Split splits script into statements using the rules of kind. Empty
// statements (only whitespace, comments or delimiters) are dropped. A COPY
// statement's data block is read into memory.
func Split(kind driver.Kind, script string) []Statement {
	sc := stringScanner(kind, script)
	out := []Statement{}
	for {
		st, err := sc.Next()
		if err != nil {
			return out
		}
		if st.CopyData != nil {
			data, _ := io.ReadAll(st.CopyData)
			st.CopyData = bytes.NewReader(data)
		}
		out = append(out, st)
	}
}

// Words returns the bare words (keywords and unquoted identifiers,
// upper-cased) of a statement, skipping strings, quoted identifiers and
// comments. At most limit words are returned (limit <= 0: all).
func Words(kind driver.Kind, text string, limit int) []string {
	out := []string{}
	sc := stringScanner(kind, text)
	sc.d.copyData = false
	sc.onWord = func(w string) {
		if limit <= 0 || len(out) < limit {
			out = append(out, w)
		}
	}
	for {
		if _, err := sc.Next(); err != nil {
			return out
		}
	}
}

// Param is one :name placeholder of a statement.
type Param struct {
	// Name is the name without the colon.
	Name string `json:"name"`
	// Start is the byte offset of the colon; End the offset after the name.
	Start int `json:"start"`
	End   int `json:"end"`
}

// Params returns the :name placeholders of text in order, skipping
// strings, quoted identifiers, comments, dollar-quoted bodies, psql
// meta-commands and variables (:'var'), casts (::type), assignments (:=),
// array slices (a[1:n]) and labels (lbl: BEGIN).
func Params(kind driver.Kind, text string) []Param {
	out := []Param{}
	sc := stringScanner(kind, text)
	sc.d.copyData = false
	sc.onParam = func(name string, start, end int) {
		out = append(out, Param{Name: name, Start: start, End: end})
	}
	for {
		if _, err := sc.Next(); err != nil {
			return out
		}
	}
}

// ParamNames returns the distinct placeholder names of text in order of
// first use (names are case-sensitive).
func ParamNames(kind driver.Kind, text string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range Params(kind, text) {
		if !seen[p.Name] {
			seen[p.Name] = true
			out = append(out, p.Name)
		}
	}
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

// fill compacts the buffer and reads more input (at least as much as is
// buffered past what the scanner keeps, so re-scanning stays linear).
func (sc *Scanner) fill() error {
	if sc.eof {
		return nil
	}
	if sc.readErr != nil {
		return sc.readErr
	}
	keep := sc.i
	if sc.start >= 0 && sc.start < keep {
		keep = sc.start
	}
	keep -= 2 // E'…' looks back two bytes
	if keep > 0 {
		sc.compact(keep)
	}
	want := max(readSize, len(sc.buf))
	if sc.copy == nil {
		if room := sc.max + 2 - len(sc.buf); room <= 0 {
			return fmt.Errorf("%w: the statement at line %d is larger than %d MiB", ErrStatementTooLarge, sc.lineNow(), sc.max>>20)
		} else {
			want = min(want, room)
		}
	} else {
		want = readSize
	}
	if cap(sc.buf)-len(sc.buf) < want {
		nb := make([]byte, len(sc.buf), len(sc.buf)+want)
		copy(nb, sc.buf)
		sc.buf = nb
	}
	got := 0
	for empty := 0; got < want; {
		n, err := sc.r.Read(sc.buf[len(sc.buf) : len(sc.buf)+want-got])
		sc.buf = sc.buf[:len(sc.buf)+n]
		got += n
		if err == io.EOF {
			sc.eof = true
			return nil
		}
		if err != nil {
			if got > 0 {
				// Use what was read first.
				sc.readErr = err
				return nil
			}
			return err
		}
		if n == 0 {
			if empty++; empty > 100 {
				return io.ErrNoProgress
			}
		} else if sc.copy != nil {
			return nil // data blocks stream: no need to wait for more
		}
	}
	return nil
}

// compact drops buf[:keep].
func (sc *Scanner) compact(keep int) {
	if sc.linePos < keep {
		sc.nl += bytes.Count(sc.buf[sc.linePos:keep], []byte{'\n'})
		sc.linePos = keep
	}
	n := copy(sc.buf, sc.buf[keep:])
	sc.buf = sc.buf[:n]
	sc.base += keep
	sc.i -= keep
	sc.linePos -= keep
	if sc.start >= 0 {
		sc.start -= keep
		sc.end -= keep
	}
	if sc.wordEnd -= keep; sc.wordEnd < 0 {
		sc.wordEnd = -1
	}
}

// lineOf returns the 1-based line of buffer offset pos (pos never goes
// backwards between calls).
func (sc *Scanner) lineOf(pos int) int {
	if pos > sc.linePos {
		sc.nl += bytes.Count(sc.buf[sc.linePos:pos], []byte{'\n'})
		sc.linePos = pos
	}
	return sc.nl + 1
}

// lineNow is the line of the current statement (or position).
func (sc *Scanner) lineNow() int {
	if sc.start >= 0 {
		return sc.startLine
	}
	return sc.lineOf(sc.i)
}

// needMore marks the step as needing more input when the stream has not
// ended, and reports whether it did.
func (sc *Scanner) needMore() bool {
	if sc.eof {
		return false
	}
	sc.short = true
	return true
}

// step consumes whitespace, a delimiter, a directive or one token.
func (sc *Scanner) step() {
	if sc.i >= len(sc.buf) {
		if sc.needMore() {
			return
		}
		sc.flush(len(sc.buf), false)
		sc.done = true
		return
	}
	if sc.d.delimiter && sc.lineStart {
		if sc.directive() || sc.short {
			return
		}
	}
	switch c := sc.buf[sc.i]; c {
	case '\n':
		sc.i++
		sc.lineStart = true
		return
	case ' ', '\t', '\r', '\f', '\v':
		sc.i++
		return
	}
	sc.lineStart = false
	if sc.d.metaCommands && sc.start < 0 && sc.buf[sc.i] == '\\' {
		sc.metaCommand()
		return
	}
	if sc.depth == 0 && sc.prefixAt(sc.i, sc.delim) {
		sc.i += len(sc.delim)
		sc.flush(sc.i, true)
		return
	}
	if sc.short {
		return
	}
	sc.token()
}

// metaCommand consumes a psql meta-command: the rest of the line.
func (sc *Scanner) metaCommand() {
	end := sc.lineEnd(sc.i)
	if sc.short {
		return
	}
	textEnd := end
	for textEnd > sc.i+1 && isSpaceOrCtl(sc.buf[textEnd-1]) {
		textEnd--
	}
	sc.content(sc.i, textEnd)
	sc.i = end
	sc.meta = true
	sc.flush(end, false)
	sc.lineStart = true
}

// prefixAt reports whether p starts at buffer offset j (more input is
// needed when the buffer ends inside a possible match).
func (sc *Scanner) prefixAt(j int, p string) bool {
	if len(sc.buf)-j < len(p) {
		if strings.HasPrefix(p, string(sc.buf[j:])) {
			sc.needMore()
		}
		return false
	}
	return string(sc.buf[j:j+len(p)]) == p
}

// flush ends the current statement; delimEnd is the offset after its
// delimiter, delimited whether a delimiter (not the end) ended it.
func (sc *Scanner) flush(delimEnd int, delimited bool) {
	if sc.start >= 0 {
		sc.out = Statement{Text: string(sc.buf[sc.start:sc.end]), Start: sc.base + sc.start, End: sc.base + sc.end,
			DelimEnd: sc.base + delimEnd, Line: sc.startLine, Meta: sc.meta}
		sc.pending = true
		sc.copyIn = delimited && sc.d.copyData && sc.fromStdin
	}
	sc.start, sc.end, sc.words, sc.depth, sc.bodyKind = -1, 0, 0, 0, bodyNone
	sc.prevBegin, sc.prevFrom, sc.fromStdin, sc.meta = false, false, false, false
}

// content marks buf[from:to] as part of the current statement.
func (sc *Scanner) content(from, to int) {
	if sc.start < 0 {
		sc.start = from
		sc.startLine = sc.lineOf(from)
	}
	sc.end = to
}

// token consumes one token at sc.i (not whitespace).
func (sc *Scanner) token() {
	s, i := sc.buf, sc.i
	c := s[i]
	next := byte(0)
	if i+1 < len(s) {
		next = s[i+1]
	} else if sc.needMore() {
		return
	}
	// afterWord: the token directly follows a word or number (no blank).
	afterWord := sc.wordEnd == i
	switch {
	case c == '-' && next == '-' && sc.d.dashNeedsSpace && i+2 >= len(s) && sc.needMore():
		return
	case c == '-' && next == '-' && (!sc.d.dashNeedsSpace || i+2 >= len(s) || isSpaceOrCtl(s[i+2])):
		sc.i = sc.lineEnd(i)
		sc.lineStart = true
		return
	case c == '#' && sc.d.hashComments:
		sc.i = sc.lineEnd(i)
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
		sc.i = sc.quoteEnd(i, '\'', escapes)
	case c == '"':
		sc.i = sc.quoteEnd(i, '"', sc.d.backslashStrings && sc.d.doubleIsString)
	case c == '`' && sc.d.backticks:
		sc.i = sc.quoteEnd(i, '`', false)
	case c == '[' && sc.d.brackets:
		if j := bytes.IndexByte(s[i+1:], ']'); j >= 0 {
			sc.i = i + j + 2
		} else {
			sc.needMore()
			sc.i = len(s)
		}
	case c == '$' && sc.d.dollarQuotes && !afterWord:
		if end, ok := sc.dollarEnd(i); ok {
			sc.i = end
		} else {
			sc.i = i + 1
		}
	case c == ':' && next == ':':
		// A PostgreSQL cast (x::int): never the start of a :name.
		sc.i = i + 2
	case c == ':' && isIdentStart(next) && (i == 0 || !(isIdent(s[i-1]) || s[i-1] == ']' || s[i-1] == ')')):
		// A :name placeholder (not a[1:n], a[i:n] or a MySQL label).
		j := i + 2
		for j < len(s) && isIdent(s[j]) && !sc.delimAt(j) {
			j++
		}
		if j == len(s) && sc.needMore() {
			return
		}
		sc.i = j
		if sc.onParam != nil {
			sc.onParam(string(s[i+1:j]), sc.base+i, sc.base+j)
		}
	case isIdentStart(c):
		j := i + 1
		for j < len(s) && isIdent(s[j]) && !sc.delimAt(j) {
			j++
		}
		if j == len(s) && sc.needMore() {
			return
		}
		sc.i = j
		sc.content(i, j)
		sc.word(string(s[i:j]))
		sc.wordEnd = j
		return
	case isDigit(c):
		j := i + 1
		for j < len(s) && (isIdent(s[j]) || s[j] == '.') && !sc.delimAt(j) {
			j++
		}
		if j == len(s) && sc.needMore() {
			return
		}
		sc.i = j
		sc.wordEnd = j // 1e'x' is not an E-string
	default:
		sc.i = i + 1
	}
	sc.content(i, sc.i)
}

// word tracks keywords for BEGIN … END bodies and COPY … FROM stdin.
func (sc *Scanner) word(w string) {
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
	if sc.d.copyData && sc.first[0] == "COPY" && sc.prevFrom && up == "STDIN" {
		sc.fromStdin = true
	}
	sc.prevBegin = up == "BEGIN"
	sc.prevFrom = up == "FROM"
}

// detectBody recognises CREATE [TEMP|TEMPORARY] TRIGGER (SQLite) and
// CREATE [OR REPLACE] FUNCTION|PROCEDURE (PostgreSQL) from the first words.
func (sc *Scanner) detectBody() int {
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
func (sc *Scanner) delimAt(j int) bool {
	return sc.delim != ";" && sc.depth == 0 && sc.prefixAt(j, sc.delim)
}

// directive handles a MySQL client DELIMITER line at sc.i (which is at the
// start of a line, possibly after blanks). It reports whether one was
// consumed.
func (sc *Scanner) directive() bool {
	s, i := sc.buf, sc.i
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	const kw = "DELIMITER"
	if len(s)-i <= len(kw) {
		sc.needMore()
		return false
	}
	if !strings.EqualFold(string(s[i:i+len(kw)]), kw) || (s[i+len(kw)] != ' ' && s[i+len(kw)] != '\t') {
		return false
	}
	end := sc.lineEnd(i)
	if sc.short {
		return false
	}
	arg := strings.Fields(string(s[i+len(kw) : end]))
	if len(arg) == 0 {
		return false
	}
	// A pending statement ends where the directive begins.
	sc.flush(i, false)
	sc.delim = arg[0]
	sc.i = end
	return true
}

// blockCommentEnd returns the offset after the comment starting at i.
func (sc *Scanner) blockCommentEnd(i int) int {
	s := sc.buf
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
	sc.needMore()
	return len(s)
}

// quoteEnd returns the offset after the quoted token starting at i. The
// quote is escaped by doubling it, and by a backslash when escapes is set.
func (sc *Scanner) quoteEnd(i int, q byte, escapes bool) int {
	s := sc.buf
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
			if j+1 == len(s) && sc.needMore() {
				return len(s)
			}
			return j + 1
		}
	}
	sc.needMore()
	return len(s)
}

// dollarEnd returns the offset after a dollar-quoted string starting at i,
// or ok=false when buf[i:] does not start a dollar quote ($1 parameters, a
// lone $).
func (sc *Scanner) dollarEnd(i int) (int, bool) {
	s := sc.buf
	j := i + 1
	if j < len(s) && isIdentStart(s[j]) {
		for j < len(s) && isIdent(s[j]) && s[j] != '$' {
			j++
		}
	}
	if j >= len(s) {
		sc.needMore()
		return 0, false
	}
	if s[j] != '$' {
		return 0, false
	}
	tag := s[i : j+1]
	if k := bytes.Index(s[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag), true
	}
	sc.needMore()
	return len(s), true
}

// lineEnd returns the offset after the newline ending the line at i (or
// the end of the buffer).
func (sc *Scanner) lineEnd(i int) int {
	if j := bytes.IndexByte(sc.buf[i:], '\n'); j >= 0 {
		return i + j + 1
	}
	sc.needMore()
	return len(sc.buf)
}

// copyReader streams the data block of a COPY … FROM stdin statement from
// the scanner's buffer: the rest of the statement's line is skipped, then
// lines are passed through until one that is exactly \. (LF or CRLF).
type copyReader struct {
	sc          *Scanner
	skipLine    bool
	atLineStart bool
	done        bool
	err         error
}

func (c *copyReader) Read(p []byte) (int, error) {
	sc := c.sc
	for {
		if c.done {
			return 0, io.EOF
		}
		if c.err != nil {
			return 0, c.err
		}
		if len(p) == 0 {
			return 0, nil
		}
		avail := sc.buf[sc.i:]
		switch {
		case c.skipLine:
			if k := bytes.IndexByte(avail, '\n'); k >= 0 {
				sc.i += k + 1
				c.skipLine = false
				continue
			}
			sc.i = len(sc.buf)
			if sc.eof {
				c.skipLine = false
				continue
			}
		case len(avail) == 0:
			if sc.eof {
				c.err = fmt.Errorf("%w (the data starts after line %d)", ErrCopyUnterminated, sc.lineOf(sc.i)-1)
				continue
			}
		default:
			if c.atLineStart && avail[0] == '\\' {
				n, more := terminator(avail, sc.eof)
				if n > 0 {
					sc.i += n
					c.done = true
					sc.lineStart = true
					continue
				}
				if more {
					break // read more, then decide
				}
			}
			n := len(avail)
			if k := bytes.IndexByte(avail, '\n'); k >= 0 {
				n = k + 1
			}
			n = copy(p, avail[:n])
			sc.i += n
			c.atLineStart = avail[n-1] == '\n'
			return n, nil
		}
		if err := sc.fill(); err != nil {
			c.err = err
			sc.err = err
		}
	}
}

// terminator returns the length of the \. end-of-data line at the start of
// b (0 if there is none); more is set when that cannot be told yet.
func terminator(b []byte, eof bool) (n int, more bool) {
	if len(b) < 2 {
		return 0, !eof
	}
	if b[1] != '.' {
		return 0, false
	}
	rest := b[2:]
	switch {
	case len(rest) == 0:
		if eof {
			return 2, false
		}
		return 0, true
	case rest[0] == '\n':
		return 3, false
	case rest[0] != '\r':
		return 0, false
	case len(rest) == 1:
		if eof {
			return 3, false
		}
		return 0, true
	case rest[1] == '\n':
		return 4, false
	}
	return 0, false
}

func isSpaceOrCtl(c byte) bool { return c <= ' ' }
func isDigit(c byte) bool      { return c >= '0' && c <= '9' }

// isIdentStart: letters, underscore and any non-ASCII byte (UTF-8
// identifiers).
func isIdentStart(c byte) bool {
	return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') || c >= 0x80
}

func isIdent(c byte) bool { return isIdentStart(c) || isDigit(c) || c == '$' }
