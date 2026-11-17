package importer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// jsonRecord is one object: its keys in order and their values.
type jsonRecord struct {
	keys []string
	vals []*string
	pos  int
}

// jsonSource reads a JSON array of objects (streamed with json.Decoder)
// or NDJSON (one object per line). start reads the first PreviewRows
// records to find the columns, and next replays them first.
type jsonSource struct {
	ndjson bool
	br     *bufio.Reader
	dec    *json.Decoder // array mode
	opened bool          // the array's '[' was read
	pos    int           // NDJSON line or array element of the last record

	header   []string
	index    map[string]int
	buffered []jsonRecord
}

func newJSONSource(br *bufio.Reader, ndjson bool) *jsonSource {
	if b, err := br.Peek(3); err == nil && bytes.Equal(b, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = br.Discard(3)
	}
	s := &jsonSource{ndjson: ndjson, br: br, index: map[string]int{}}
	if !ndjson {
		s.dec = json.NewDecoder(br)
	}
	return s
}

func (s *jsonSource) width() int     { return len(s.header) }
func (s *jsonSource) elements() bool { return !s.ndjson }

// rowErr is a RowError at the current record.
func (s *jsonSource) rowErr(msg string, err error) *RowError {
	if s.ndjson {
		return &RowError{Line: s.pos, Message: msg, Err: err}
	}
	return &RowError{Element: s.pos, Message: msg, Err: err}
}

// start reads up to PreviewRows records; the columns are their keys in
// first-seen order.
func (s *jsonSource) start() ([]string, error) {
	for len(s.buffered) < PreviewRows {
		rec, err := s.read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, k := range rec.keys {
			if _, ok := s.index[k]; !ok {
				s.index[k] = len(s.header)
				s.header = append(s.header, k)
			}
		}
		s.buffered = append(s.buffered, rec)
	}
	return s.header, nil
}

// next returns the next record's values in header order (missing keys are
// NULL).
func (s *jsonSource) next() ([]*string, int, error) {
	var rec jsonRecord
	if len(s.buffered) > 0 {
		rec, s.buffered = s.buffered[0], s.buffered[1:]
	} else {
		var err error
		if rec, err = s.read(); err != nil {
			return nil, s.pos, err
		}
	}
	out := make([]*string, len(s.header))
	for i, k := range rec.keys {
		j, ok := s.index[k]
		if !ok {
			return nil, rec.pos, s.rowErr(fmt.Sprintf("the key %q does not appear in the first %d records, so it has no column", k, PreviewRows), nil)
		}
		out[j] = rec.vals[i]
	}
	return out, rec.pos, nil
}

// read parses the next object.
func (s *jsonSource) read() (jsonRecord, error) {
	if s.ndjson {
		return s.readLine()
	}
	if !s.opened {
		tok, err := s.dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return jsonRecord{}, io.EOF
			}
			return jsonRecord{}, s.rowErr("JSON syntax error: "+err.Error(), err)
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return jsonRecord{}, s.rowErr("a JSON file must hold an array of objects (use NDJSON for one object per line)", nil)
		}
		s.opened = true
	}
	if !s.dec.More() {
		if _, err := s.dec.Token(); err != nil { // ']'
			return jsonRecord{}, s.rowErr("JSON syntax error: "+err.Error(), err)
		}
		if _, err := s.dec.Token(); !errors.Is(err, io.EOF) {
			return jsonRecord{}, s.rowErr("unexpected data after the JSON array", err)
		}
		return jsonRecord{}, io.EOF
	}
	s.pos++
	return s.object(s.dec)
}

// readLine parses the next non-blank NDJSON line.
func (s *jsonSource) readLine() (jsonRecord, error) {
	for {
		line, err := s.br.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			if errors.Is(err, io.EOF) {
				return jsonRecord{}, io.EOF
			}
			return jsonRecord{}, err
		}
		s.pos++
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if !utf8.Valid(line) {
			return jsonRecord{}, s.rowErr("the file is not valid UTF-8", nil)
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		rec, rerr := s.object(dec)
		if rerr != nil {
			return jsonRecord{}, rerr
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return jsonRecord{}, s.rowErr("unexpected data after the object (NDJSON has one object per line)", err)
		}
		return rec, nil
	}
}

// object parses one JSON object from dec.
func (s *jsonSource) object(dec *json.Decoder) (jsonRecord, error) {
	syntax := func(err error) (jsonRecord, error) {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return jsonRecord{}, s.rowErr("JSON syntax error: "+err.Error(), err)
	}
	tok, err := dec.Token()
	if err != nil {
		return syntax(err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return jsonRecord{}, s.rowErr("expected a JSON object, found "+describe(tok), nil)
	}
	rec := jsonRecord{pos: s.pos}
	seen := map[string]int{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return syntax(err)
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return syntax(err)
		}
		v, err := rawValue(raw)
		if err != nil {
			return syntax(err)
		}
		if i, dup := seen[key]; dup { // the last one wins, as in most parsers
			rec.vals[i] = v
			continue
		}
		seen[key] = len(rec.keys)
		rec.keys = append(rec.keys, key)
		rec.vals = append(rec.vals, v)
	}
	if _, err := dec.Token(); err != nil { // '}'
		return syntax(err)
	}
	return rec, nil
}

// rawValue converts a JSON value to field text: strings as they are,
// numbers as their literal text, booleans as true/false, null as NULL and
// objects/arrays as compact JSON.
func rawValue(raw json.RawMessage) (*string, error) {
	var v string
	switch raw[0] {
	case 'n':
		return nil, nil
	case '"':
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
	case '{', '[':
		var b bytes.Buffer
		if err := json.Compact(&b, raw); err != nil {
			return nil, err
		}
		v = b.String()
	default: // numbers, true, false
		v = string(raw)
	}
	return &v, nil
}

// describe names a JSON token for an error message.
func describe(tok json.Token) string {
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			return "an array"
		}
		return string(t)
	case string:
		return "a string"
	case float64, json.Number:
		return "a number"
	case bool:
		return "a boolean " + strconv.FormatBool(t)
	case nil:
		return "null"
	}
	return fmt.Sprint(tok)
}
