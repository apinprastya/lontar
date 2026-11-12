// Package importer loads CSV files into an existing table.
//
// Parsing uses encoding/csv. The quote character can be changed: since
// encoding/csv only knows '"', the input bytes of the chosen quote
// character and '"' are swapped before parsing and swapped back in every
// field, which is exact because both are ASCII. Only UTF-8 input is
// accepted (a byte order mark is skipped); invalid UTF-8 is an error with
// its line number.
//
// Rows are inserted with multi-row INSERT statements of bound parameters,
// converted per column exactly like grid edits (edits.BindText: text bound
// as-is on PostgreSQL and parsed by the server; integers and booleans as
// numbers on MySQL/MariaDB/SQLite). Binary columns take hex. Everything
// runs in one transaction: any error rolls back every row, and it names the
// CSV line (and the column when it can be told). A failed batch is retried
// row by row inside savepoints to find the failing line.
package importer

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"lontar/internal/driver"
	"lontar/internal/edits"
	"lontar/internal/values"

	"github.com/jackc/pgx/v5/pgconn"
)

// Options controls parsing.
type Options struct {
	// Delimiter is one character (default ","; "\t" for TSV).
	Delimiter string `json:"delimiter"`
	// Header: the first record (after Skip) holds column names.
	Header bool `json:"header"`
	// Quote is one character (default `"`).
	Quote string `json:"quote"`
	// Encoding must be "utf-8" (or empty).
	Encoding string `json:"encoding"`
	// NullMarker: a field equal to it becomes NULL ("" = no marker).
	NullMarker string `json:"nullMarker"`
	// EmptyAsNull turns empty fields into NULL.
	EmptyAsNull bool `json:"emptyAsNull"`
	// Skip is the number of records to skip before the header or data.
	Skip int `json:"skip"`
}

// PreviewRows is how many data rows Preview returns.
const PreviewRows = 50

// Errors.
var (
	ErrOptions  = errors.New("invalid import options")
	ErrReadOnly = errors.New("the connection is in read-only safe mode; importing is not allowed")
	ErrMapping  = errors.New("invalid column mapping")
)

// Mapping maps a CSV field (0-based Source) to a table column.
type Mapping struct {
	Source int    `json:"source"`
	Column string `json:"column"`
}

// RowError is an import failure at a CSV line. Nothing was imported.
type RowError struct {
	// Line is the 1-based line in the file where the record starts (0
	// when unknown).
	Line int `json:"line"`
	// Column is the target column, when known.
	Column  string `json:"column"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

func (e *RowError) Error() string {
	where := "line " + strconv.Itoa(e.Line)
	if e.Column != "" {
		where += ", column " + e.Column
	}
	return where + ": " + e.Message + ". Nothing was imported."
}

func (e *RowError) Unwrap() error { return e.Err }

// reader parses a CSV stream with opts.
type reader struct {
	csv   *csv.Reader
	quote byte // swapped with '"' when not '"'
	null  string
	empty bool
	cols  int // fields of the widest record seen
}

// swapReader exchanges the bytes a and b.
type swapReader struct {
	r    io.Reader
	a, b byte
}

func (s swapReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	for i := range p[:n] {
		switch p[i] {
		case s.a:
			p[i] = s.b
		case s.b:
			p[i] = s.a
		}
	}
	return n, err
}

func single(s, def, what string) (byte, error) {
	if s == "" {
		s = def
	}
	if s == `\t` {
		s = "\t"
	}
	if len(s) != 1 || s[0] >= utf8.RuneSelf || s == "\n" || s == "\r" {
		return 0, fmt.Errorf("%w: the %s must be one ASCII character", ErrOptions, what)
	}
	return s[0], nil
}

func newReader(r io.Reader, o Options) (*reader, error) {
	switch strings.ToLower(strings.ReplaceAll(o.Encoding, "-", "")) {
	case "", "utf8":
	default:
		return nil, fmt.Errorf("%w: only UTF-8 files are supported", ErrOptions)
	}
	delim, err := single(o.Delimiter, ",", "delimiter")
	if err != nil {
		return nil, err
	}
	quote, err := single(o.Quote, `"`, "quote character")
	if err != nil {
		return nil, err
	}
	if delim == quote {
		return nil, fmt.Errorf("%w: the delimiter and the quote character must differ", ErrOptions)
	}
	if o.Skip < 0 {
		return nil, fmt.Errorf("%w: rows to skip must not be negative", ErrOptions)
	}
	br := bufio.NewReaderSize(r, 64<<10)
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = br.Discard(3)
	}
	var in io.Reader = br
	rd := &reader{quote: '"', null: o.NullMarker, empty: o.EmptyAsNull}
	if quote != '"' {
		in = swapReader{r: br, a: quote, b: '"'}
		rd.quote = quote
		if delim == '"' {
			// The swap turns the '"' delimiter into the quote byte.
			delim = quote
		}
	}
	// encoding/csv turns \r\n inside quoted fields into \n; protect
	// carriage returns in quoted fields (see crReader).
	in = &crReader{r: bufio.NewReaderSize(in, 64<<10)}
	c := csv.NewReader(in)
	c.Comma = rune(delim)
	c.FieldsPerRecord = -1
	rd.csv = c
	return rd, nil
}

// next returns the next record's fields (nil for NULL) and its line.
func (r *reader) next() ([]*string, int, error) {
	rec, err := r.csv.Read()
	if err != nil {
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			return nil, pe.StartLine, &RowError{Line: pe.StartLine, Message: "CSV syntax error: " + pe.Err.Error(), Err: err}
		}
		return nil, 0, err
	}
	line, _ := r.csv.FieldPos(0)
	out := make([]*string, len(rec))
	for i, f := range rec {
		if strings.IndexByte(f, crMark) >= 0 {
			f = strings.ReplaceAll(f, string([]byte{crMark}), "\r")
		}
		if r.quote != '"' {
			f = swapBack(f, r.quote)
		}
		if !utf8.ValidString(f) {
			return nil, line, &RowError{Line: line, Message: "the file is not valid UTF-8"}
		}
		if (r.null != "" && f == r.null) || (r.empty && f == "") {
			continue
		}
		v := f
		out[i] = &v
	}
	r.cols = max(r.cols, len(rec))
	return out, line, nil
}

// crMark stands in for '\r' inside quoted fields while encoding/csv
// parses (it never occurs in UTF-8, which is all the importer accepts).
const crMark = 0xff

// crReader passes the CSV stream through, tracking '"' quoting: a '\r'
// inside a quoted field becomes crMark (restored after parsing, so CRLF
// inside values survives), and a "\r\n" line ending outside quotes
// becomes "\n".
type crReader struct {
	r       *bufio.Reader
	inQuote bool
}

func (c *crReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		b, err := c.r.ReadByte()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		switch {
		case b == crMark:
			// Not UTF-8 either way: keep it invalid (0xfe) so the field's
			// UTF-8 check reports the right line.
			b = 0xfe
		case b == '"':
			c.inQuote = !c.inQuote
		case b == '\r' && c.inQuote:
			b = crMark
		case b == '\r':
			if next, err := c.r.Peek(1); err == nil && next[0] == '\n' {
				continue
			}
		}
		p[n] = b
		n++
	}
	return n, nil
}

func swapBack(s string, q byte) string {
	if strings.IndexByte(s, q) < 0 && strings.IndexByte(s, '"') < 0 {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		switch c {
		case q:
			b[i] = '"'
		case '"':
			b[i] = q
		}
	}
	return string(b)
}

// start skips records and reads the header.
func (r *reader) start(o Options) ([]string, error) {
	for i := 0; i < o.Skip; i++ {
		if _, _, err := r.next(); err != nil {
			if errors.Is(err, io.EOF) {
				r.cols = 0
				return nil, nil
			}
			return nil, err
		}
	}
	r.cols = 0 // skipped records do not count for the width
	if !o.Header {
		return nil, nil
	}
	rec, _, err := r.next()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h := make([]string, len(rec))
	for i, f := range rec {
		if f != nil {
			h[i] = strings.TrimSpace(*f)
		}
	}
	return h, nil
}

// TargetColumn describes a column of the target table.
type TargetColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	HasDefault bool   `json:"hasDefault"`
	IsPK       bool   `json:"isPK"`
}

// Preview is the start of a file and a suggested mapping.
type Preview struct {
	// Headers names every CSV field: the header row, or "Column 1", ….
	Headers []string `json:"headers"`
	// Rows are up to PreviewRows data rows (null = NULL).
	Rows    [][]*string    `json:"rows"`
	Columns []TargetColumn `json:"columns"`
	// Mapping maps CSV fields to columns with the same name (ignoring
	// case); unmatched fields are left out.
	Mapping []Mapping `json:"mapping"`
}

// Engine is what the importer needs from a driver.
type Engine = edits.Engine

// Preview reads the start of r and suggests a mapping onto schema.table.
func PreviewFile(ctx context.Context, db *sql.DB, e Engine, path string, o Options, schema, table string) (*Preview, error) {
	t, err := edits.LoadTable(ctx, db, e, schema, table)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return preview(f, o, t)
}

func preview(in io.Reader, o Options, t edits.Table) (*Preview, error) {
	r, err := newReader(in, o)
	if err != nil {
		return nil, err
	}
	header, err := r.start(o)
	if err != nil {
		return nil, err
	}
	p := &Preview{Rows: [][]*string{}, Mapping: []Mapping{}}
	for len(p.Rows) < PreviewRows {
		rec, _, err := r.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		p.Rows = append(p.Rows, rec)
	}
	width := max(len(header), r.cols)
	p.Headers = make([]string, width)
	for i := range p.Headers {
		if i < len(header) && header[i] != "" {
			p.Headers[i] = header[i]
		} else {
			p.Headers[i] = "Column " + strconv.Itoa(i+1)
		}
	}
	for _, c := range t.Columns {
		p.Columns = append(p.Columns, TargetColumn{Name: c.Name, Type: c.DataType, Nullable: c.Nullable, HasDefault: c.Default != nil, IsPK: c.IsPrimaryKey})
	}
	used := map[string]bool{}
	for i, h := range header {
		for _, c := range t.Columns {
			if !used[c.Name] && strings.EqualFold(strings.TrimSpace(h), c.Name) {
				p.Mapping = append(p.Mapping, Mapping{Source: i, Column: c.Name})
				used[c.Name] = true
				break
			}
		}
	}
	return p, nil
}

// Request describes an import.
type Request struct {
	Path    string    `json:"path"`
	Options Options   `json:"options"`
	Schema  string    `json:"schema"`
	Table   string    `json:"table"`
	Mapping []Mapping `json:"mapping"`
	// BatchSize is the rows per INSERT (default 500; capped so a batch
	// stays below the engines' parameter limits).
	BatchSize int `json:"batchSize"`
}

// RunOptions are the session-level settings of an import.
type RunOptions struct {
	// ReadOnly refuses the import (read-only safe mode).
	ReadOnly bool
}

// Result reports a finished import.
type Result struct {
	Rows int64 `json:"rows"`
}

// Progress is called after every committed batch with the rows inserted
// so far (the transaction commits at the end).
type Progress func(rows int64)

// DefaultBatch is the default number of rows per INSERT.
const DefaultBatch = 500

// maxParams keeps a batch below every engine's bound-parameter limit
// (SQLite 32766, PostgreSQL and MySQL 65535).
const maxParams = 30000

// plan is a validated mapping.
type plan struct {
	t    edits.Table
	cols []driver.Column
	src  []int
	bin  []bool
}

func makePlan(t edits.Table, m []Mapping) (*plan, error) {
	if len(m) == 0 {
		return nil, fmt.Errorf("%w: map at least one CSV column to a table column", ErrMapping)
	}
	p := &plan{t: t}
	seen := map[string]bool{}
	for _, mp := range m {
		i := slices.IndexFunc(t.Columns, func(c driver.Column) bool { return c.Name == mp.Column })
		switch {
		case i < 0:
			return nil, fmt.Errorf("%w: unknown column %q", ErrMapping, mp.Column)
		case seen[mp.Column]:
			return nil, fmt.Errorf("%w: column %q is mapped twice", ErrMapping, mp.Column)
		case mp.Source < 0:
			return nil, fmt.Errorf("%w: invalid CSV column %d", ErrMapping, mp.Source)
		}
		seen[mp.Column] = true
		c := t.Columns[i]
		p.cols = append(p.cols, c)
		p.src = append(p.src, mp.Source)
		p.bin = append(p.bin, values.Categorize(c.DataType) == values.CatBinary)
	}
	return p, nil
}

// row is one parsed record ready to bind.
type row struct {
	line int
	args []any
	text []*string
}

func (p *plan) bind(kind driver.Kind, rec []*string, line int) (row, error) {
	r := row{line: line, args: make([]any, len(p.cols)), text: make([]*string, len(p.cols))}
	for i, c := range p.cols {
		s := p.src[i]
		if s >= len(rec) {
			return row{}, &RowError{Line: line, Column: c.Name,
				Message: fmt.Sprintf("the record has %d fields, but this column needs field %d", len(rec), s+1)}
		}
		f := rec[s]
		r.text[i] = f
		switch {
		case f == nil:
			r.args[i] = nil
		case p.bin[i]:
			b, err := edits.DecodeHex(*f)
			if err != nil {
				return row{}, &RowError{Line: line, Column: c.Name, Message: "binary values must be hex", Err: err}
			}
			r.args[i] = b
		default:
			r.args[i] = edits.BindText(kind, c, *f)
		}
	}
	return r, nil
}

// insertSQL builds INSERT INTO t (cols) VALUES (…), … for n rows.
func (p *plan) insertSQL(d driver.Dialect, n int) string {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	if p.t.Schema != "" {
		b.WriteString(d.QuoteIdent(p.t.Schema))
		b.WriteString(".")
	}
	b.WriteString(d.QuoteIdent(p.t.Name))
	b.WriteString(" (")
	for i, c := range p.cols {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(d.QuoteIdent(c.Name))
	}
	b.WriteString(") VALUES ")
	k := 0
	for r := 0; r < n; r++ {
		if r > 0 {
			b.WriteString(", ")
		}
		b.WriteString("(")
		for i := range p.cols {
			if i > 0 {
				b.WriteString(", ")
			}
			k++
			b.WriteString(d.Placeholder(k))
		}
		b.WriteString(")")
	}
	return b.String()
}

// Run imports req.Path into the table in one transaction.
func Run(ctx context.Context, db *sql.DB, e Engine, req Request, ro RunOptions, progress Progress) (*Result, error) {
	if ro.ReadOnly {
		return nil, ErrReadOnly
	}
	t, err := edits.LoadTable(ctx, db, e, req.Schema, req.Table)
	if err != nil {
		return nil, err
	}
	p, err := makePlan(t, req.Mapping)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(req.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := newReader(f, req.Options)
	if err != nil {
		return nil, err
	}
	if _, err := r.start(req.Options); err != nil {
		return nil, err
	}
	batch := req.BatchSize
	if batch <= 0 {
		batch = DefaultBatch
	}
	batch = max(1, min(batch, maxParams/len(p.cols)))

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	in := &inserter{ctx: ctx, tx: tx, e: e, p: p}
	var total int64
	rows := make([]row, 0, batch)
	flush := func() error {
		if len(rows) == 0 {
			return nil
		}
		if err := in.insert(rows); err != nil {
			return err
		}
		total += int64(len(rows))
		rows = rows[:0]
		if progress != nil {
			progress(total)
		}
		return nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rec, line, err := r.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		rw, err := p.bind(e.Kind(), rec, line)
		if err != nil {
			return nil, err
		}
		rows = append(rows, rw)
		if len(rows) == batch {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit failed: %w. Nothing was imported", err)
	}
	committed = true
	return &Result{Rows: total}, nil
}

type inserter struct {
	ctx context.Context
	tx  *sql.Tx
	e   Engine
	p   *plan
}

func (in *inserter) exec(rows []row) error {
	args := make([]any, 0, len(rows)*len(in.p.cols))
	for _, r := range rows {
		args = append(args, r.args...)
	}
	_, err := in.tx.ExecContext(in.ctx, in.p.insertSQL(in.e, len(rows)), edits.ExecArgs(in.e.Kind(), args)...)
	return err
}

// insert inserts a batch. When it fails, the batch is retried row by row
// inside savepoints to find the failing line.
func (in *inserter) insert(rows []row) error {
	if len(rows) == 1 {
		if err := in.exec(rows); err != nil {
			return in.rowError(rows[0], err)
		}
		return nil
	}
	if _, err := in.tx.ExecContext(in.ctx, "SAVEPOINT lontar_import"); err != nil {
		return err
	}
	err := in.exec(rows)
	if err == nil {
		_, err = in.tx.ExecContext(in.ctx, "RELEASE SAVEPOINT lontar_import")
		return err
	}
	if in.ctx.Err() != nil {
		return in.ctx.Err()
	}
	if _, rerr := in.tx.ExecContext(in.ctx, "ROLLBACK TO SAVEPOINT lontar_import"); rerr != nil {
		return in.rowError(rows[0], err)
	}
	for _, r := range rows {
		if err := in.exec([]row{r}); err != nil {
			return in.rowError(r, err)
		}
	}
	// No single row failed (should not happen): report the batch.
	return in.rowError(rows[0], err)
}

var (
	columnWord   = regexp.MustCompile(`(?i)column\s+(\S+)`)
	sqliteCol    = regexp.MustCompile(`constraint failed: (\S+)`)
	pgInputValue = regexp.MustCompile(`: "((?:[^"]|"")*)"`)
)

// lastName returns the last part of a possibly qualified, quoted name
// ("\x60db\x60.\x60t\x60.\x60col\x60", 'col', t.col).
func lastName(tok string) string {
	tok = strings.TrimRight(tok, ",.:;")
	if i := strings.LastIndex(tok, "."); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.Trim(tok, "`'\"")
}

// rowError wraps a database error for r, naming the column when the
// error does: PostgreSQL's column field or a quoted value that only one
// field has, MySQL "column 'x'", SQLite "constraint failed: t.x".
func (in *inserter) rowError(r row, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	re := &RowError{Line: r.line, Message: err.Error(), Err: err}
	var pe *pgconn.PgError
	known := func(name string) bool {
		return slices.ContainsFunc(in.p.cols, func(c driver.Column) bool { return c.Name == name })
	}
	switch {
	case errors.As(err, &pe) && pe.ColumnName != "":
		re.Column = pe.ColumnName
	case errors.As(err, &pe):
		if m := pgInputValue.FindStringSubmatch(pe.Message); m != nil {
			val := strings.ReplaceAll(m[1], `""`, `"`)
			var hit []string
			for i, t := range r.text {
				if t != nil && *t == val {
					hit = append(hit, in.p.cols[i].Name)
				}
			}
			if len(hit) == 1 {
				re.Column = hit[0]
			}
		}
	default:
		msg := err.Error()
		var toks []string
		for _, m := range sqliteCol.FindAllStringSubmatch(msg, -1) {
			toks = append(toks, m[1])
		}
		for _, m := range columnWord.FindAllStringSubmatch(msg, -1) {
			toks = append(toks, m[1])
		}
		for _, tok := range toks {
			if name := lastName(tok); known(name) {
				re.Column = name
				break
			}
		}
	}
	return re
}
