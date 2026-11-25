package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	dbdriver "lontar/internal/driver"
	"lontar/internal/sqlsplit"
)

// ExplainOptions controls Explain.
type ExplainOptions struct {
	// Analyze runs the statement and reports actual rows and times (not
	// on SQLite; refused for statements that write).
	Analyze bool `json:"analyze"`
	// BaseOffset is the position of the SQL text inside the editor
	// (UTF-16 code units); it is added to the reported error position.
	BaseOffset int `json:"baseOffset"`
	// Params are the values of the statement's :name placeholders (see
	// ExecOptions.Params).
	Params map[string]ParamValue `json:"params"`
}

// Plan formats.
const (
	PlanFormatJSON = "json"
	PlanFormatText = "text"
)

// PlanProp is one property of a plan node, in the engine's own words.
type PlanProp struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// PlanNode is one operation of a query plan. Numbers the engine does not
// report are null. Row counts are per loop (as the engines report them);
// TimeMs is the inclusive time of the node over all its loops.
type PlanNode struct {
	Op         string     `json:"op"`
	Object     string     `json:"object"`
	Detail     string     `json:"detail"`
	EstRows    *float64   `json:"estRows"`
	ActualRows *float64   `json:"actualRows"`
	Loops      *float64   `json:"loops"`
	Cost       *float64   `json:"cost"`
	TimeMs     *float64   `json:"timeMs"`
	Props      []PlanProp `json:"props"`
	Children   []PlanNode `json:"children"`

	// selfMs is a time that covers the node alone (MariaDB reports
	// those); accumulate turns it into the inclusive TimeMs.
	selfMs *float64
}

// Plan is the result of Explain: the engine's plan parsed into one tree
// shape shared by every engine, plus the raw output.
type Plan struct {
	Engine   string `json:"engine"`
	Analyzed bool   `json:"analyzed"`
	// SQL is the EXPLAIN statement that was run.
	SQL         string     `json:"sql"`
	PlanningMs  *float64   `json:"planningMs"`
	ExecutionMs *float64   `json:"executionMs"`
	Nodes       []PlanNode `json:"nodes"`
	// Raw is the engine's output (RawFormat "json" or "text").
	Raw       string   `json:"raw"`
	RawFormat string   `json:"rawFormat"`
	Notices   []string `json:"notices"`
	// Error is the database error, with its position in the editor.
	Error        *ExecError `json:"error"`
	Cancelled    bool       `json:"cancelled"`
	SessionReset bool       `json:"sessionReset"`
	Tx           TxInfo     `json:"tx"`
	DurationMs   float64    `json:"durationMs"`
	// NeedsParams reports that nothing was run: these :name placeholders
	// have no value in ExplainOptions.Params.
	NeedsParams []string `json:"needsParams,omitempty"`
}

// ExplainError codes.
const (
	ExplainOneStatement       = "one_statement"
	ExplainNotExplainable     = "not_explainable"
	ExplainAlreadyExplain     = "already_explain"
	ExplainAnalyzeWrite       = "analyze_write"
	ExplainAnalyzeUnsupported = "analyze_unsupported"
)

// ExplainError is why a statement cannot be explained; nothing was sent
// to the database. Kind is always "explain" so the UI can recognise it.
type ExplainError struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ExplainError) Error() string { return e.Message }

func explainErr(code, msg string) *ExplainError {
	return &ExplainError{Kind: "explain", Code: code, Message: msg}
}

// explainKeywords start the statements Explain accepts.
var explainKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "TABLE": true, "VALUES": true,
	"INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "MERGE": true,
}

// prepareExplain checks sqlText for Explain: it must hold exactly one
// explainable statement, and Analyze only runs reads.
func prepareExplain(kind dbdriver.Kind, sqlText string, opts ExplainOptions) (sqlsplit.Statement, error) {
	stmts := sqlsplit.Split(kind, sqlText)
	if len(stmts) != 1 {
		return sqlsplit.Statement{}, explainErr(ExplainOneStatement, fmt.Sprintf("Explain works on one statement; this text has %d. Put the cursor in one statement or select it.", len(stmts)))
	}
	st := stmts[0]
	if err := explainable(kind, st.Text); err != nil {
		return st, err
	}
	return st, analyzeAllowed(kind, st.Text, opts)
}

// explainable reports whether a statement can be explained.
func explainable(kind dbdriver.Kind, text string) error {
	words := sqlsplit.Words(kind, text, 1)
	first := ""
	if len(words) > 0 {
		first = words[0]
	}
	switch {
	case first == "EXPLAIN" || first == "DESCRIBE" || first == "DESC":
		return explainErr(ExplainAlreadyExplain, "This statement is already an EXPLAIN; run it normally to see its output.")
	case !explainKeywords[first]:
		return explainErr(ExplainNotExplainable, "Only queries (SELECT, WITH, TABLE, VALUES) and INSERT, UPDATE, DELETE, REPLACE or MERGE have a query plan.")
	}
	return nil
}

// analyzeAllowed refuses Explain analyze where it would write or is not
// supported: analyze runs the statement.
func analyzeAllowed(kind dbdriver.Kind, text string, opts ExplainOptions) error {
	if !opts.Analyze {
		return nil
	}
	if kind == dbdriver.SQLite {
		return explainErr(ExplainAnalyzeUnsupported, "SQLite has no EXPLAIN ANALYZE; use Explain.")
	}
	if IsWrite(kind, text) {
		return explainErr(ExplainAnalyzeWrite, "Explain analyze runs the statement, so it only runs reads. Use Explain to see the plan of a statement that writes.")
	}
	return nil
}

// explainSQL returns the EXPLAIN statement for stmt and the length of the
// prefix put in front of it (to map error positions back to stmt).
func explainSQL(kind dbdriver.Kind, stmt string, opts ExplainOptions) (string, int) {
	var prefix string
	switch kind {
	case dbdriver.Postgres:
		prefix = "EXPLAIN (FORMAT JSON) "
		if opts.Analyze {
			prefix = "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "
		}
	case dbdriver.MySQL:
		prefix = "EXPLAIN FORMAT=JSON "
		if opts.Analyze {
			prefix = "EXPLAIN ANALYZE "
		}
	case dbdriver.MariaDB:
		prefix = "EXPLAIN FORMAT=JSON "
		if opts.Analyze {
			prefix = "ANALYZE FORMAT=JSON "
		}
	default:
		prefix = "EXPLAIN QUERY PLAN "
	}
	return prefix + stmt, len(prefix)
}

// shiftPosition maps a byte offset in the EXPLAIN statement to one in the
// user's statement; an offset inside the prefix points at its start.
func shiftPosition(b, prefix int) int {
	if b < prefix {
		return 0
	}
	return b - prefix
}

// sqliteQPRow is one row of SQLite's EXPLAIN QUERY PLAN.
type sqliteQPRow struct {
	ID, Parent int
	Detail     string
}

// parsePlan parses an engine's EXPLAIN output into p. A parse failure
// keeps the raw output and adds a notice.
func parsePlan(p *Plan, kind dbdriver.Kind, analyze bool, raw string, lite []sqliteQPRow) {
	var err error
	p.Raw, p.RawFormat = raw, PlanFormatJSON
	switch {
	case kind == dbdriver.Postgres:
		p.Nodes, p.PlanningMs, p.ExecutionMs, err = parsePostgresJSON(raw)
	case kind == dbdriver.MySQL && analyze:
		p.RawFormat = PlanFormatText
		p.Nodes, err = parseMySQLTree(raw)
	case kind == dbdriver.MySQL || kind == dbdriver.MariaDB:
		p.Nodes, p.PlanningMs, p.ExecutionMs, err = parseMySQLJSON(raw)
	default:
		p.RawFormat = PlanFormatText
		p.Nodes = parseSQLiteQP(lite)
		p.Raw = sqliteRaw(p.Nodes)
	}
	if err != nil {
		p.Nodes = []PlanNode{}
		p.Notices = append(p.Notices, "The plan could not be read ("+err.Error()+"); the Raw view shows the engine's output.")
	}
}

// ---- Ordered JSON ----------------------------------------------------------

// jsonKV is one member of a JSON object; jsonObj keeps the members in
// document order (the engines' order is the natural reading order).
type jsonKV struct {
	k string
	v any
}

type jsonObj []jsonKV

func (o jsonObj) get(k string) (any, bool) {
	for _, kv := range o {
		if kv.k == k {
			return kv.v, true
		}
	}
	return nil, false
}

func (o jsonObj) str(k string) string {
	v, ok := o.get(k)
	if !ok || v == nil {
		return ""
	}
	s, _ := scalarString(v)
	return s
}

func (o jsonObj) num(k string) *float64 {
	v, _ := o.get(k)
	return toNum(v)
}

func (o jsonObj) obj(k string) jsonObj {
	v, _ := o.get(k)
	ob, _ := v.(jsonObj)
	return ob
}

// decodeOrdered decodes JSON keeping object member order. Numbers stay
// json.Number.
func decodeOrdered(data string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	v, err := readJSON(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the plan")
	}
	return v, nil
}

func readJSON(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch d {
	case '{':
		o := jsonObj{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}
			o = append(o, jsonKV{kt.(string), v})
		}
		_, err := dec.Token()
		return o, err
	case '[':
		a := []any{}
		for dec.More() {
			v, err := readJSON(dec)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := dec.Token()
		return a, err
	}
	return nil, fmt.Errorf("unexpected %v", d)
}

// scalarString formats a JSON scalar; ok is false for objects and arrays.
func scalarString(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "null", true
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

// toNum reads a number (or a numeric string, as MySQL writes costs).
func toNum(v any) *float64 {
	var s string
	switch x := v.(type) {
	case json.Number:
		s = x.String()
	case string:
		s = x
	default:
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return nil
	}
	return &f
}

// propValue formats any JSON value for a property: scalars as is, arrays
// of scalars comma-separated, everything else as compact JSON.
func propValue(v any) string {
	if s, ok := scalarString(v); ok {
		return s
	}
	if a, ok := v.([]any); ok {
		parts := make([]string, 0, len(a))
		simple := true
		for _, x := range a {
			s, ok := scalarString(x)
			if !ok {
				simple = false
				break
			}
			parts = append(parts, s)
		}
		if simple {
			return strings.Join(parts, ", ")
		}
	}
	var buf bytes.Buffer
	writeCompact(&buf, v)
	return buf.String()
}

func writeCompact(buf *bytes.Buffer, v any) {
	switch x := v.(type) {
	case jsonObj:
		buf.WriteByte('{')
		for i, kv := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			k, _ := json.Marshal(kv.k)
			buf.Write(k)
			buf.WriteByte(':')
			writeCompact(buf, kv.v)
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCompact(buf, e)
		}
		buf.WriteByte(']')
	case string:
		b, _ := json.Marshal(x)
		buf.Write(b)
	default:
		s, _ := scalarString(x)
		buf.WriteString(s)
	}
}

func newNode(op string) PlanNode {
	return PlanNode{Op: op, Props: []PlanProp{}, Children: []PlanNode{}}
}

func joinNonEmpty(sep string, parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func mul(a, b *float64) *float64 {
	if a == nil || b == nil {
		return nil
	}
	v := *a * *b
	return &v
}

// ---- PostgreSQL ------------------------------------------------------------

// parsePostgresJSON parses EXPLAIN (FORMAT JSON).
func parsePostgresJSON(raw string) (nodes []PlanNode, planning, execution *float64, err error) {
	v, err := decodeOrdered(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil, nil, nil, errors.New("expected a JSON array")
	}
	top, ok := arr[0].(jsonObj)
	if !ok {
		return nil, nil, nil, errors.New("expected a JSON object")
	}
	plan := top.obj("Plan")
	if plan == nil {
		return nil, nil, nil, errors.New("no Plan in the output")
	}
	return []PlanNode{pgNode(plan)}, top.num("Planning Time"), top.num("Execution Time"), nil
}

// pgDetailKeys give a node's one-line detail (the first one present).
var pgDetailKeys = []string{"Hash Cond", "Merge Cond", "Index Cond", "Recheck Cond", "Join Filter", "Filter", "Sort Key", "Group Key", "One-Time Filter"}

func pgNode(o jsonObj) PlanNode {
	n := newNode(o.str("Node Type"))
	if jt := o.str("Join Type"); jt != "" && jt != "Inner" {
		if strings.Contains(n.Op, "Join") {
			n.Op = strings.Replace(n.Op, "Join", jt+" Join", 1)
		} else {
			n.Op += " " + jt + " Join"
		}
	}
	rel, alias, index := o.str("Relation Name"), o.str("Alias"), o.str("Index Name")
	switch {
	case rel != "":
		n.Object = rel
		if alias != "" && alias != rel {
			n.Object += " " + alias
		}
	case o.str("CTE Name") != "":
		n.Object = o.str("CTE Name")
	case o.str("Function Name") != "":
		n.Object = o.str("Function Name")
	case index != "":
		n.Object, index = index, ""
	case alias != "":
		n.Object = alias
	}
	var cond string
	for _, k := range pgDetailKeys {
		if v, ok := o.get(k); ok {
			cond = propValue(v)
			break
		}
	}
	using := ""
	if index != "" {
		using = "using " + index
	}
	n.Detail = joinNonEmpty(" ", o.str("Subplan Name"), using, cond)
	n.EstRows = o.num("Plan Rows")
	n.ActualRows = o.num("Actual Rows")
	n.Loops = o.num("Actual Loops")
	n.Cost = o.num("Total Cost")
	n.TimeMs = mul(o.num("Actual Total Time"), n.Loops)
	for _, kv := range o {
		if kv.k == "Plans" {
			if kids, ok := kv.v.([]any); ok {
				for _, k := range kids {
					if ko, ok := k.(jsonObj); ok {
						n.Children = append(n.Children, pgNode(ko))
					}
				}
			}
			continue
		}
		n.Props = append(n.Props, PlanProp{kv.k, propValue(kv.v)})
	}
	return n
}

// ---- MySQL / MariaDB JSON ----------------------------------------------------

// mysqlOps are the JSON members that are plan operations, with their names.
var mysqlOps = map[string]string{
	"ordering_operation":            "Sort",
	"filesort":                      "Sort",
	"grouping_operation":            "Group",
	"duplicates_removal":            "Remove duplicates",
	"temporary_table":               "Temporary table",
	"union_result":                  "Union",
	"materialized_from_subquery":    "Materialize",
	"materialization":               "Materialize",
	"block-nl-join":                 "Block nested loop join",
	"windowing":                     "Window",
	"buffer_result":                 "Buffer result",
	"read_sorted_file":              "Read sorted file",
	"range-checked-for-each-record": "Range checked for each record",
	"duplicates_weedout":            "Duplicate weedout",
}

// mysqlAccess names the access types of a table.
var mysqlAccess = map[string]string{
	"ALL":             "Full table scan",
	"index":           "Full index scan",
	"range":           "Index range scan",
	"ref":             "Index lookup",
	"eq_ref":          "Unique index lookup",
	"ref_or_null":     "Index lookup or NULL",
	"const":           "Constant row",
	"system":          "System table",
	"fulltext":        "Full-text search",
	"index_merge":     "Index merge",
	"unique_subquery": "Unique subquery lookup",
	"index_subquery":  "Index subquery lookup",
}

// parseMySQLJSON parses EXPLAIN FORMAT=JSON of MySQL and MariaDB, and
// MariaDB's ANALYZE FORMAT=JSON (r_* members hold the actual values).
func parseMySQLJSON(raw string) (nodes []PlanNode, planning, execution *float64, err error) {
	v, err := decodeOrdered(raw)
	if err != nil {
		return nil, nil, nil, err
	}
	top, ok := v.(jsonObj)
	if !ok {
		return nil, nil, nil, errors.New("expected a JSON object")
	}
	if _, ok := top.get("query_block"); !ok {
		return nil, nil, nil, errors.New("no query_block in the output")
	}
	nodes = mysqlWalk(top)
	for i := range nodes {
		accumulate(&nodes[i])
	}
	planning = top.obj("query_optimization").num("r_total_time_ms")
	if qb := top.obj("query_block"); qb != nil {
		execution = qb.num("r_total_time_ms")
	}
	return nodes, planning, execution, nil
}

// hasOps reports whether v contains plan operations.
func hasOps(v any) bool {
	switch x := v.(type) {
	case jsonObj:
		for _, kv := range x {
			if kv.k == "query_block" || kv.k == "table" || mysqlOps[kv.k] != "" || hasOps(kv.v) {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if hasOps(e) {
				return true
			}
		}
	}
	return false
}

// mysqlWalk returns the operations among o's members.
func mysqlWalk(o jsonObj) []PlanNode {
	out := []PlanNode{}
	for _, kv := range o {
		switch {
		case kv.k == "query_block" || kv.k == "table" || mysqlOps[kv.k] != "":
			if ob, ok := kv.v.(jsonObj); ok {
				out = append(out, mysqlNode(kv.k, ob))
			}
		case kv.k == "nested_loop":
			kids := mysqlList(kv.v)
			if len(kids) > 1 {
				n := newNode("Nested loop")
				n.Children = kids
				out = append(out, n)
			} else {
				out = append(out, kids...)
			}
		default:
			if _, isObj := kv.v.(jsonObj); !isObj && hasOps(kv.v) {
				// query_specifications, attached_subqueries, subqueries, …
				out = append(out, mysqlList(kv.v)...)
			} else if isObj && hasOps(kv.v) {
				n := newNode(humanize(kv.k))
				fillMySQL(&n, kv.v.(jsonObj))
				out = append(out, n)
			}
		}
	}
	return out
}

func mysqlList(v any) []PlanNode {
	out := []PlanNode{}
	if a, ok := v.([]any); ok {
		for _, e := range a {
			if eo, ok := e.(jsonObj); ok {
				out = append(out, mysqlWalk(eo)...)
			}
		}
	}
	return out
}

func humanize(k string) string {
	s := strings.NewReplacer("_", " ", "-", " ").Replace(k)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// fillMySQL adds o's plain members as properties (nested objects without
// operations flattened as "parent.key") and its operations as children.
func fillMySQL(n *PlanNode, o jsonObj) {
	var add func(prefix string, o jsonObj)
	add = func(prefix string, o jsonObj) {
		for _, kv := range o {
			if hasOps(kv.v) || kv.k == "query_block" || kv.k == "table" || mysqlOps[kv.k] != "" || kv.k == "nested_loop" {
				continue
			}
			if sub, ok := kv.v.(jsonObj); ok {
				add(prefix+kv.k+".", sub)
				continue
			}
			n.Props = append(n.Props, PlanProp{prefix + kv.k, propValue(kv.v)})
		}
	}
	add("", o)
	n.Children = append(n.Children, mysqlWalk(o)...)
}

func mysqlNode(key string, o jsonObj) PlanNode {
	var n PlanNode
	switch key {
	case "query_block":
		n = newNode("Query block")
		if id := o.str("select_id"); id != "" {
			n.Op += " #" + id
		}
		n.Detail = joinNonEmpty(" ", o.str("operation"), o.str("message"))
		n.Cost = o.obj("cost_info").num("query_cost")
		if n.Cost == nil {
			n.Cost = o.num("cost")
		}
		n.Loops = o.num("r_loops")
		n.TimeMs = o.num("r_total_time_ms")
	case "table":
		access := o.str("access_type")
		n = newNode(mysqlAccess[access])
		if n.Op == "" {
			n.Op = joinNonEmpty(" ", "Table", access)
		}
		n.Object = o.str("table_name")
		using := ""
		if k := o.str("key"); k != "" {
			using = "using " + k
		}
		n.Detail = joinNonEmpty("; ", joinNonEmpty(" ", using), o.str("attached_condition"), o.str("message"))
		n.EstRows = o.num("rows_examined_per_scan")
		if n.EstRows == nil {
			n.EstRows = o.num("rows")
		}
		n.ActualRows = o.num("r_rows")
		n.Loops = o.num("r_loops")
		n.Cost = o.obj("cost_info").num("prefix_cost")
		if n.Cost == nil {
			n.Cost = o.num("cost")
		}
		n.selfMs = sumNums(o.num("r_table_time_ms"), o.num("r_other_time_ms"))
	default:
		n = newNode(mysqlOps[key])
		n.Object = o.str("table_name")
		n.Detail = joinNonEmpty(" ", o.str("sort_key"), o.str("join_type"), o.str("attached_condition"))
		n.ActualRows = o.num("r_output_rows")
		n.Loops = o.num("r_loops")
		n.selfMs = sumNums(o.num("r_total_time_ms"), o.num("r_other_time_ms"), o.num("r_unpack_time_ms"))
	}
	fillMySQL(&n, o)
	return n
}

func sumNums(ns ...*float64) *float64 {
	var sum float64
	found := false
	for _, n := range ns {
		if n != nil {
			sum += *n
			found = true
		}
	}
	if !found {
		return nil
	}
	return &sum
}

// accumulate makes TimeMs inclusive where the engine reported a node's
// own time only (MariaDB): own time plus the children's.
func accumulate(n *PlanNode) *float64 {
	var kids []*float64
	for i := range n.Children {
		kids = append(kids, accumulate(&n.Children[i]))
	}
	if n.TimeMs == nil && (n.selfMs != nil || sumNums(kids...) != nil) {
		n.TimeMs = sumNums(append(kids, n.selfMs)...)
	}
	return n.TimeMs
}

// ---- MySQL EXPLAIN ANALYZE (TREE) --------------------------------------------

// treeNum is a number in MySQL's tree output ("0.85", "1e+6", "534e-6").
const treeNum = `[0-9]+(?:\.[0-9]+)?(?:e[+-]?[0-9]+)?`

var (
	treeEstRe    = regexp.MustCompile(`\(cost=(` + treeNum + `)(?:\.\.(` + treeNum + `))? rows=(` + treeNum + `)\)`)
	treeActualRe = regexp.MustCompile(`\(actual time=(` + treeNum + `)\.\.(` + treeNum + `) rows=(` + treeNum + `) loops=([0-9]+)\)`)
	treeNeverRe  = regexp.MustCompile(`\(never executed\)`)
	treeOnRe     = regexp.MustCompile(`^(.+?) on (<[^>]*>|\S+)(?: (.+))?$`)
	treeParenRe  = regexp.MustCompile(`^([^(]+?) (\(.*\))$`)
)

func parseF(s string) *float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// tnode builds a tree before it is copied into PlanNode values.
type tnode struct {
	n    PlanNode
	kids []*tnode
}

func (t *tnode) build() PlanNode {
	n := t.n
	n.Children = make([]PlanNode, 0, len(t.kids))
	for _, k := range t.kids {
		n.Children = append(n.Children, k.build())
	}
	return n
}

// parseMySQLTree parses MySQL's EXPLAIN ANALYZE output: one "-> op" line
// per node, nested by indentation.
func parseMySQLTree(raw string) ([]PlanNode, error) {
	type level struct {
		indent int
		t      *tnode
	}
	var roots []*tnode
	var stack []level
	var last *tnode
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "->") {
			if last == nil {
				return nil, errors.New("expected a line starting with ->")
			}
			last.n.Detail = joinNonEmpty(" ", last.n.Detail, trimmed)
			continue
		}
		indent := strings.Index(line, "->")
		t := &tnode{n: treeNode(strings.TrimSpace(trimmed[2:]))}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, t)
		} else {
			p := stack[len(stack)-1].t
			p.kids = append(p.kids, t)
		}
		stack = append(stack, level{indent, t})
		last = t
	}
	if len(roots) == 0 {
		return nil, errors.New("empty plan")
	}
	out := make([]PlanNode, len(roots))
	for i, r := range roots {
		out[i] = r.build()
	}
	return out, nil
}

func treeNode(text string) PlanNode {
	n := newNode("")
	cut := len(text)
	if m := treeEstRe.FindStringSubmatchIndex(text); m != nil {
		cut = min(cut, m[0])
		total := text[m[2]:m[3]]
		if m[4] >= 0 {
			total = text[m[4]:m[5]]
		}
		n.Cost = parseF(total)
		n.EstRows = parseF(text[m[6]:m[7]])
	}
	if m := treeActualRe.FindStringSubmatch(text); m != nil {
		cut = min(cut, strings.Index(text, m[0]))
		n.ActualRows = parseF(m[3])
		n.Loops = parseF(m[4])
		n.TimeMs = mul(parseF(m[2]), n.Loops)
	} else if loc := treeNeverRe.FindStringIndex(text); loc != nil {
		cut = min(cut, loc[0])
		zero := 0.0
		n.Loops = &zero
	}
	desc := strings.TrimSpace(text[:cut])
	n.Props = append(n.Props, PlanProp{"Description", text})
	if i := strings.Index(desc, ": "); i > 0 && !strings.Contains(desc[:i], "(") {
		n.Op, n.Detail = desc[:i], desc[i+2:]
	} else if m := treeOnRe.FindStringSubmatch(desc); m != nil {
		n.Op, n.Object, n.Detail = m[1], m[2], m[3]
	} else if m := treeParenRe.FindStringSubmatch(desc); m != nil {
		n.Op, n.Detail = m[1], m[2]
	} else {
		n.Op = desc
	}
	return n
}

// ---- SQLite EXPLAIN QUERY PLAN ------------------------------------------------

// parseSQLiteQP builds the tree of EXPLAIN QUERY PLAN rows (id, parent).
func parseSQLiteQP(rows []sqliteQPRow) []PlanNode {
	byID := map[int]*tnode{}
	var roots []*tnode
	for _, r := range rows {
		t := &tnode{n: sqliteNode(r.Detail)}
		byID[r.ID] = t
		if p, ok := byID[r.Parent]; ok && r.Parent != r.ID {
			p.kids = append(p.kids, t)
		} else {
			roots = append(roots, t)
		}
	}
	out := make([]PlanNode, 0, len(roots))
	for _, r := range roots {
		out = append(out, r.build())
	}
	return out
}

func sqliteNode(detail string) PlanNode {
	n := newNode(detail)
	n.Props = append(n.Props, PlanProp{"detail", detail})
	for _, op := range []string{"SCAN", "SEARCH"} {
		rest, ok := strings.CutPrefix(detail, op+" ")
		if !ok {
			continue
		}
		rest = strings.TrimPrefix(rest, "TABLE ")
		n.Op = op
		n.Object, n.Detail, _ = strings.Cut(rest, " ")
		break
	}
	return n
}

// sqliteRaw renders the plan like the sqlite3 shell's .eqp output.
func sqliteRaw(nodes []PlanNode) string {
	var b strings.Builder
	b.WriteString("QUERY PLAN\n")
	var walk func(ns []PlanNode, indent string)
	walk = func(ns []PlanNode, indent string) {
		for i, n := range ns {
			branch, next := "|--", "|  "
			if i == len(ns)-1 {
				branch, next = "`--", "   "
			}
			b.WriteString(indent + branch + propOr(n, "detail", n.Op) + "\n")
			walk(n.Children, indent+next)
		}
	}
	walk(nodes, "")
	return strings.TrimRight(b.String(), "\n")
}

func propOr(n PlanNode, key, def string) string {
	for _, p := range n.Props {
		if p.Key == key {
			return p.Value
		}
	}
	return def
}
