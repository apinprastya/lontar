package ddl

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	numberRe   = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+)?$`)
	bitHexRe   = regexp.MustCompile(`^(?i)(b'[01]*'|x'[0-9a-f]*'|0x[0-9a-f]+)$`)
	onUpdateRe = regexp.MustCompile(`(?i)on update (\S+(\(\d*\))?)`)
)

// mysqlRawDefault renders the default of an existing column as
// information_schema reports it ("" when there is none): MariaDB reports
// SQL (quoted literals, NULL, expressions); MySQL reports literal values
// unquoted and marks expressions with DEFAULT_GENERATED.
func (p *plan) mysqlRawDefault(c MySQLColumn) string {
	if c.Default == nil {
		return ""
	}
	d := *c.Default
	if p.f == MariaDB {
		switch {
		case d == "NULL", strings.HasPrefix(d, "'"), numberRe.MatchString(d), bitHexRe.MatchString(d), timeFuncRe.MatchString(d):
			return d
		}
		return "(" + d + ")"
	}
	low := strings.ToLower(c.Extra)
	if strings.Contains(low, "default_generated") || timeFuncRe.MatchString(d) {
		if timeFuncRe.MatchString(d) {
			return d
		}
		// information_schema escapes quotes inside expressions.
		return "(" + strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(d) + ")"
	}
	if bitHexRe.MatchString(d) && strings.HasPrefix(strings.ToLower(c.Type), "bit") {
		return d
	}
	return p.lit(d)
}

// mysqlColumnDef renders a column definition: the change applied over
// the current raw definition (nil raw: a new column).
func (p *plan) mysqlColumnDef(c column, raw *MySQLColumn) string {
	var r MySQLColumn
	if raw != nil {
		r = *raw
	}
	typ := r.Type
	if c.typ != "" {
		typ = c.typ
	}
	parts := []string{p.q(c.name), typ}
	switch {
	case c.ch.Collation != nil && *c.ch.Collation != "":
		parts = append(parts, "COLLATE "+*c.ch.Collation)
	case c.ch.Collation == nil && c.typ == "" && r.Collation != "":
		parts = append(parts, "COLLATE "+r.Collation)
	}
	low := strings.ToLower(r.Extra)
	gen, stored := c.gen, c.ch.Generated != nil && c.ch.Generated.Stored
	if raw != nil && r.GenExpr != "" {
		gen = strings.NewReplacer(`\'`, `'`, `\\`, `\`).Replace(r.GenExpr)
		stored = strings.Contains(low, "stored") || strings.Contains(low, "persistent")
	}
	if gen != "" {
		kind := "VIRTUAL"
		if stored {
			kind = "STORED"
		}
		g := gen
		if !strings.HasPrefix(g, "(") || !strings.HasSuffix(g, ")") {
			g = "(" + g + ")"
		}
		parts = append(parts, "GENERATED ALWAYS AS "+g+" "+kind)
	}
	nullable := raw == nil || r.Nullable
	if c.ch.Nullable != nil {
		nullable = *c.ch.Nullable
	}
	switch {
	case gen != "" && p.f == MariaDB:
		// MariaDB has no NULL / NOT NULL on generated columns.
	case nullable:
		parts = append(parts, "NULL")
	default:
		parts = append(parts, "NOT NULL")
	}
	if gen == "" {
		def := ""
		if c.defSet {
			def = c.def
		} else if raw != nil {
			def = p.mysqlRawDefault(r)
		}
		if def != "" {
			parts = append(parts, "DEFAULT "+def)
		}
		if m := onUpdateRe.FindStringSubmatch(r.Extra); m != nil {
			parts = append(parts, "ON UPDATE "+m[1])
		}
	}
	auto := strings.Contains(low, "auto_increment")
	if c.ch.AutoIncrement != nil {
		auto = *c.ch.AutoIncrement
	}
	if auto {
		parts = append(parts, "AUTO_INCREMENT")
	}
	if strings.Contains(low, "invisible") {
		parts = append(parts, "INVISIBLE")
	}
	comment := r.Comment
	if c.ch.Comment != nil {
		comment = *c.ch.Comment
	}
	if comment != "" {
		parts = append(parts, "COMMENT "+p.lit(comment))
	}
	return strings.Join(parts, " ")
}

// mysqlIndex renders an index definition (without ADD / CREATE).
func (p *plan) mysqlIndex(x index) string {
	kind := "INDEX "
	switch {
	case x.ch.Method == "FULLTEXT" || x.ch.Method == "SPATIAL":
		kind = x.ch.Method + " INDEX "
	case x.ch.Unique:
		kind = "UNIQUE INDEX "
	}
	s := kind + p.q(x.ch.Name) + " (" + strings.Join(x.cols, ", ") + ")"
	if x.ch.Method == "BTREE" || x.ch.Method == "HASH" {
		s += " USING " + x.ch.Method
	}
	return s
}

func (p *plan) mysql() ([]Statement, error) {
	tc := p.in.Change
	t := p.table()
	var out []Statement
	add := func(section string, i int, sql string) { out = append(out, stmt(section, i, sql)) }

	if tc.Create {
		var defs []string
		for _, c := range p.cols {
			defs = append(defs, p.mysqlColumnDef(c, nil))
		}
		if p.pk != nil && len(*p.pk) > 0 {
			defs = append(defs, "PRIMARY KEY ("+p.list(*p.pk)+")")
		}
		for _, x := range p.idx {
			defs = append(defs, p.mysqlIndex(x))
		}
		for _, x := range p.fks {
			defs = append(defs, p.fkClause(x))
		}
		for _, x := range p.checks {
			defs = append(defs, p.checkClause(x))
		}
		s := "CREATE TABLE " + t + " (\n  " + strings.Join(defs, ",\n  ") + "\n)"
		if p.comment != nil && *p.comment != "" {
			s += " COMMENT=" + p.lit(*p.comment)
		}
		add(SectionTable, -1, s)
		return out, nil
	}

	// Foreign keys are dropped and added in statements of their own:
	// MySQL refuses some drop+add combinations in one ALTER TABLE.
	var drops []string
	for _, x := range p.fks {
		if x.ch.Op == OpDrop {
			drops = append(drops, "DROP FOREIGN KEY "+p.q(x.ch.Name))
		}
	}
	if len(drops) > 0 {
		add(SectionFKs, -1, "ALTER TABLE "+t+" "+strings.Join(drops, ", "))
	}
	for i, name := range p.trigs {
		add(SectionTriggers, i, "DROP TRIGGER "+p.q(tc.Schema)+"."+p.q(name))
	}

	var clauses []string
	for _, x := range p.checks {
		if x.ch.Op == OpDrop {
			if p.f == MariaDB {
				clauses = append(clauses, "DROP CONSTRAINT "+p.q(x.ch.Name))
			} else {
				clauses = append(clauses, "DROP CHECK "+p.q(x.ch.Name))
			}
		}
	}
	for _, x := range p.idx {
		if x.ch.Op == OpDrop {
			clauses = append(clauses, "DROP INDEX "+p.q(x.ch.Name))
		}
	}
	if p.pk != nil && p.hasPK() {
		clauses = append(clauses, "DROP PRIMARY KEY")
	}
	for _, c := range p.cols {
		if c.ch.Op == OpDrop {
			clauses = append(clauses, "DROP COLUMN "+p.q(c.ch.Name))
		}
	}
	for _, c := range p.cols {
		if c.ch.Op != OpAlter {
			continue
		}
		raw, ok := p.in.Extra.MySQLColumns[c.ch.Name]
		if !ok {
			return nil, notFound(SectionColumns, c.i, "name", fmt.Sprintf("Column %q was not found.", c.ch.Name))
		}
		if c.name != c.ch.Name {
			clauses = append(clauses, "CHANGE COLUMN "+p.q(c.ch.Name)+" "+p.mysqlColumnDef(c, &raw))
		} else {
			clauses = append(clauses, "MODIFY COLUMN "+p.mysqlColumnDef(c, &raw))
		}
	}
	for _, c := range p.cols {
		if c.ch.Op == OpAdd {
			clauses = append(clauses, "ADD COLUMN "+p.mysqlColumnDef(c, nil))
		}
	}
	if p.pk != nil && len(*p.pk) > 0 {
		clauses = append(clauses, "ADD PRIMARY KEY ("+p.list(*p.pk)+")")
	}
	for _, x := range p.idx {
		switch x.ch.Op {
		case OpAdd:
			clauses = append(clauses, "ADD "+p.mysqlIndex(x))
		case OpRename:
			clauses = append(clauses, "RENAME INDEX "+p.q(x.ch.Name)+" TO "+p.q(x.ch.NewName))
		}
	}
	for _, x := range p.checks {
		if x.ch.Op == OpAdd {
			clauses = append(clauses, "ADD "+p.checkClause(x))
		}
	}
	if p.comment != nil {
		clauses = append(clauses, "COMMENT = "+p.lit(*p.comment))
	}
	if len(clauses) > 0 {
		add(SectionTable, -1, "ALTER TABLE "+t+"\n  "+strings.Join(clauses, ",\n  "))
	}

	var adds []string
	for _, x := range p.fks {
		if x.ch.Op == OpAdd {
			adds = append(adds, "ADD "+p.fkClause(x))
		}
	}
	if len(adds) > 0 {
		add(SectionFKs, -1, "ALTER TABLE "+t+" "+strings.Join(adds, ", "))
	}
	if p.rename != "" {
		add(SectionTable, -1, "RENAME TABLE "+t+" TO "+p.q(tc.Schema)+"."+p.q(p.rename))
	}
	return out, nil
}
