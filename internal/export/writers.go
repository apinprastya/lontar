package export

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"lontar/internal/driver"
	"lontar/internal/values"
)

// Format is an export file format.
type Format string

// Formats.
const (
	FormatCSV  Format = "csv"
	FormatJSON Format = "json"
	FormatSQL  Format = "sql"
)

// CSV null representations (CSVOptions.Null).
const (
	NullEmpty  = "empty"
	NullBackN  = `\N`
	NullCustom = "custom"
)

// CSVOptions configures CSV output. Fields are quoted only when needed: when
// they contain the delimiter, the quote character, a line break or leading
// or trailing space, or when an unquoted field would read as NULL (an empty
// string with Null "empty", the literal \N or custom NULL text). Binary
// values are written as lowercase hex.
type CSVOptions struct {
	Delimiter string `json:"delimiter"` // one character, default ","
	Header    bool   `json:"header"`
	Quote     string `json:"quote"` // one character, default `"`
	Null      string `json:"null"`  // empty | \N | custom
	NullText  string `json:"nullText"`
	CRLF      bool   `json:"crlf"`
	BOM       bool   `json:"bom"`
}

// Bytes encodings for JSON.
const (
	BytesHex    = "hex"
	BytesBase64 = "base64"
)

// JSONOptions configures JSON output: an array of objects (keyed by
// column name; duplicate names get a _2, _3 … suffix) or NDJSON (one object
// per line). NULL is null. Numbers that a JSON number cannot hold exactly
// (integers beyond ±2^53, decimals, NaN) are strings, and every number is a
// string with NumbersAsStrings. JSON documents are strings with their exact
// text. Dates and times are strings; bytes are hex or base64 strings.
type JSONOptions struct {
	NDJSON           bool   `json:"ndjson"`
	NumbersAsStrings bool   `json:"numbersAsStrings"`
	Bytes            string `json:"bytes"` // hex | base64
}

// SQLOptions configures SQL INSERT output (see literal.go for escaping).
type SQLOptions struct {
	// Table is the INSERT target; Schema qualifies it when set.
	Table  string `json:"table"`
	Schema string `json:"schema"`
	// BatchSize is the number of rows per INSERT (default 100).
	BatchSize int `json:"batchSize"`
	// CreateTable adds the table's DDL before the INSERTs (whole-table
	// exports only; filled in by the caller from TableDDL).
	CreateTable bool `json:"createTable"`
	// DDL is the CREATE TABLE text written when CreateTable is set.
	DDL string `json:"-"`
	// OverridingSystemValue adds OVERRIDING SYSTEM VALUE (PostgreSQL
	// tables with GENERATED ALWAYS identity columns).
	OverridingSystemValue bool `json:"-"`
	// SkipColumns are left out of table exports (generated columns).
	SkipColumns []string `json:"-"`
}

// Options selects the format and its options.
type Options struct {
	Format Format      `json:"format"`
	CSV    CSVOptions  `json:"csv"`
	JSON   JSONOptions `json:"json"`
	SQL    SQLOptions  `json:"sql"`
}

// Column is an exported column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Writer writes rows in one format.
type Writer interface {
	Begin(cols []Column) error
	Row(vals []Value) error
	End() error
}

// MaxBatch bounds SQLOptions.BatchSize.
const MaxBatch = 10000

// ErrOptions is wrapped by invalid option errors.
var ErrOptions = errors.New("invalid export options")

func oneRune(s, def, what string) (rune, error) {
	if s == "" {
		s = def
	}
	if utf8.RuneCountInString(s) != 1 || s == "\n" || s == "\r" {
		return 0, fmt.Errorf("%w: the %s must be a single character", ErrOptions, what)
	}
	r, _ := utf8.DecodeRuneInString(s)
	return r, nil
}

// NewWriter returns a writer for opts writing to w. kind selects the SQL
// dialect.
func NewWriter(w io.Writer, kind driver.Kind, d driver.Dialect, opts Options) (Writer, error) {
	switch opts.Format {
	case FormatCSV:
		delim, err := oneRune(opts.CSV.Delimiter, ",", "delimiter")
		if err != nil {
			return nil, err
		}
		quote, err := oneRune(opts.CSV.Quote, `"`, "quote character")
		if err != nil {
			return nil, err
		}
		if quote == delim {
			return nil, fmt.Errorf("%w: the delimiter and the quote character must differ", ErrOptions)
		}
		null := ""
		switch opts.CSV.Null {
		case NullEmpty, "":
		case NullBackN:
			null = `\N`
		case NullCustom:
			null = opts.CSV.NullText
		default:
			return nil, fmt.Errorf("%w: unknown NULL representation %q", ErrOptions, opts.CSV.Null)
		}
		eol := "\n"
		if opts.CSV.CRLF {
			eol = "\r\n"
		}
		return &csvWriter{w: w, o: opts.CSV, delim: string(delim), quote: string(quote), null: null, eol: eol}, nil
	case FormatJSON:
		switch opts.JSON.Bytes {
		case "", BytesHex, BytesBase64:
		default:
			return nil, fmt.Errorf("%w: unknown bytes encoding %q", ErrOptions, opts.JSON.Bytes)
		}
		return &jsonWriter{w: w, o: opts.JSON}, nil
	case FormatSQL:
		o := opts.SQL
		if o.BatchSize <= 0 {
			o.BatchSize = 100
		}
		if o.BatchSize > MaxBatch {
			o.BatchSize = MaxBatch
		}
		if strings.TrimSpace(o.Table) == "" {
			return nil, fmt.Errorf("%w: the INSERT target table is required", ErrOptions)
		}
		return &sqlWriter{w: w, kind: kind, d: d, o: o}, nil
	}
	return nil, fmt.Errorf("%w: unknown format %q", ErrOptions, opts.Format)
}

// bom is the UTF-8 byte order mark.
const bom = "\xef\xbb\xbf"

// ---- CSV ------------------------------------------------------------------

type csvWriter struct {
	w                  io.Writer
	o                  CSVOptions
	delim, quote, null string
	eol                string
	line               strings.Builder
}

func (c *csvWriter) Begin(cols []Column) error {
	if c.o.BOM {
		if _, err := io.WriteString(c.w, bom); err != nil {
			return err
		}
	}
	if !c.o.Header {
		return nil
	}
	c.line.Reset()
	for i, col := range cols {
		if i > 0 {
			c.line.WriteString(c.delim)
		}
		c.field(col.Name, false)
	}
	return c.flush()
}

// field appends one field, quoting it when needed. A real value that
// would read back as NULL is quoted.
func (c *csvWriter) field(s string, isValue bool) {
	need := strings.Contains(s, c.delim) || strings.Contains(s, c.quote) || strings.ContainsAny(s, "\r\n") ||
		(s != "" && (s[0] == ' ' || s[len(s)-1] == ' ' || s[0] == '\t' || s[len(s)-1] == '\t')) ||
		(isValue && s == c.null)
	if !need {
		c.line.WriteString(s)
		return
	}
	c.line.WriteString(c.quote)
	c.line.WriteString(strings.ReplaceAll(s, c.quote, c.quote+c.quote))
	c.line.WriteString(c.quote)
}

func (c *csvWriter) flush() error {
	c.line.WriteString(c.eol)
	_, err := io.WriteString(c.w, c.line.String())
	return err
}

func (c *csvWriter) Row(vals []Value) error {
	c.line.Reset()
	for i, v := range vals {
		if i > 0 {
			c.line.WriteString(c.delim)
		}
		switch v.Kind {
		case values.Null:
			c.line.WriteString(c.null)
		case values.Bool:
			c.field(strconv.FormatBool(v.Bool), true)
		case values.Bytes:
			c.field(hex.EncodeToString(v.Bytes), true)
		default:
			c.field(v.Text, true)
		}
	}
	return c.flush()
}

func (c *csvWriter) End() error { return nil }

// ---- JSON -----------------------------------------------------------------

type jsonWriter struct {
	w    io.Writer
	o    JSONOptions
	keys [][]byte
	n    int
	buf  []byte
}

func (j *jsonWriter) Begin(cols []Column) error {
	seen := map[string]int{}
	j.keys = make([][]byte, len(cols))
	for i, c := range cols {
		name := c.Name
		seen[name]++
		if n := seen[name]; n > 1 {
			name = fmt.Sprintf("%s_%d", name, n)
		}
		k, _ := json.Marshal(name)
		j.keys[i] = k
	}
	if !j.o.NDJSON {
		_, err := io.WriteString(j.w, "[")
		return err
	}
	return nil
}

func (j *jsonWriter) Row(vals []Value) error {
	b := j.buf[:0]
	if !j.o.NDJSON {
		if j.n > 0 {
			b = append(b, ',')
		}
		b = append(b, '\n')
	}
	b = append(b, '{')
	for i, v := range vals {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, j.keys[i]...)
		b = append(b, ':')
		b = j.value(b, v)
	}
	b = append(b, '}')
	if j.o.NDJSON {
		b = append(b, '\n')
	}
	j.buf = b
	j.n++
	_, err := j.w.Write(b)
	return err
}

func jsonString(b []byte, s string) []byte {
	q, _ := json.Marshal(s)
	return append(b, q...)
}

func (j *jsonWriter) value(b []byte, v Value) []byte {
	switch v.Kind {
	case values.Null:
		return append(b, "null"...)
	case values.Bool:
		return strconv.AppendBool(b, v.Bool)
	case values.Num:
		if v.Safe && !j.o.NumbersAsStrings {
			return append(b, v.Text...)
		}
		return jsonString(b, v.Text)
	case values.Bytes:
		if j.o.Bytes == BytesBase64 {
			return jsonString(b, base64.StdEncoding.EncodeToString(v.Bytes))
		}
		return jsonString(b, hex.EncodeToString(v.Bytes))
	}
	return jsonString(b, v.Text)
}

func (j *jsonWriter) End() error {
	if j.o.NDJSON {
		return nil
	}
	end := "]\n"
	if j.n > 0 {
		end = "\n]\n"
	}
	_, err := io.WriteString(j.w, end)
	return err
}

// ---- SQL ------------------------------------------------------------------

type sqlWriter struct {
	w      io.Writer
	kind   driver.Kind
	d      driver.Dialect
	o      SQLOptions
	prefix string
	inRow  int
	b      strings.Builder
}

func (s *sqlWriter) Begin(cols []Column) error {
	var h strings.Builder
	h.WriteString("-- Exported by Lontar\n")
	if s.kind == driver.MySQL || s.kind == driver.MariaDB {
		h.WriteString("SET NAMES utf8mb4;\n")
	}
	if s.o.CreateTable && strings.TrimSpace(s.o.DDL) != "" {
		ddl := strings.TrimRight(s.o.DDL, " \t\r\n")
		h.WriteString("\n")
		h.WriteString(ddl)
		if !strings.HasSuffix(ddl, ";") {
			h.WriteString(";")
		}
		h.WriteString("\n")
	}
	h.WriteString("\n")
	if _, err := io.WriteString(s.w, h.String()); err != nil {
		return err
	}
	var p strings.Builder
	p.WriteString("INSERT INTO ")
	if s.o.Schema != "" {
		p.WriteString(s.d.QuoteIdent(s.o.Schema))
		p.WriteString(".")
	}
	p.WriteString(s.d.QuoteIdent(s.o.Table))
	p.WriteString(" (")
	for i, c := range cols {
		if i > 0 {
			p.WriteString(", ")
		}
		p.WriteString(s.d.QuoteIdent(c.Name))
	}
	p.WriteString(")")
	if s.o.OverridingSystemValue && s.kind == driver.Postgres {
		p.WriteString(" OVERRIDING SYSTEM VALUE")
	}
	p.WriteString(" VALUES\n")
	s.prefix = p.String()
	return nil
}

func (s *sqlWriter) Row(vals []Value) error {
	if s.inRow == 0 {
		s.b.Reset()
		s.b.WriteString(s.prefix)
	} else {
		s.b.WriteString(",\n")
	}
	s.b.WriteString("  (")
	for i, v := range vals {
		if i > 0 {
			s.b.WriteString(", ")
		}
		s.b.WriteString(Literal(s.kind, v))
	}
	s.b.WriteString(")")
	s.inRow++
	if s.inRow >= s.o.BatchSize {
		return s.flush()
	}
	return nil
}

func (s *sqlWriter) flush() error {
	if s.inRow == 0 {
		return nil
	}
	s.b.WriteString(";\n")
	s.inRow = 0
	_, err := io.WriteString(s.w, s.b.String())
	return err
}

func (s *sqlWriter) End() error { return s.flush() }

// bufferedWriter wraps w for the writers (they issue many small writes).
func bufferedWriter(w io.Writer) *bufio.Writer { return bufio.NewWriterSize(w, 64<<10) }
