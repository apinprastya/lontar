package ddl

import (
	"fmt"
	"slices"
	"strings"

	"lontar/internal/driver"
)

// Extra is engine metadata the builders need beyond driver.TableDetail.
type Extra struct {
	// PKName is the primary key constraint (PostgreSQL).
	PKName string
	// ConstraintIndexes maps an index that backs a UNIQUE or EXCLUDE
	// constraint to the constraint (PostgreSQL): it is dropped with DROP
	// CONSTRAINT.
	ConstraintIndexes map[string]string
	// MySQLColumns are the raw column definitions (MySQL/MariaDB), so a
	// MODIFY restates what is not changed exactly.
	MySQLColumns map[string]MySQLColumn
}

// MySQLColumn is a row of information_schema.COLUMNS.
type MySQLColumn struct {
	Type      string
	Nullable  bool
	Default   *string
	Extra     string
	Collation string
	Comment   string
	GenExpr   string
}

// RefLookup returns the columns of a referenced table, or a not-found error.
type RefLookup func(schema, table string) ([]string, error)

// Input is everything Build needs.
type Input struct {
	Kind    driver.Kind
	Dialect driver.Dialect
	Caps    Caps
	Lit     Literals
	// Current is the table as it is now (nil with Create).
	Current *driver.TableDetail
	Extra   Extra
	Change  TableChange
	// Refs resolves foreign key targets (nil: not checked).
	Refs RefLookup
}

// column is a validated column change.
type column struct {
	i      int
	ch     ColumnChange
	cur    *driver.ColumnDetail
	name   string // final name
	typ    string // rendered type ("" unchanged)
	def    string // rendered default
	defSet bool
	using  string
	gen    string
}

type index struct {
	i    int
	ch   IndexChange
	cols []string // rendered key parts
	pred string
}

type fkey struct {
	i         int
	ch        FKChange
	onUpdate  string
	onDelete  string
	refSchema string
}

type check struct {
	i    int
	ch   CheckChange
	expr string
}

// plan is a validated change set.
type plan struct {
	in      Input
	f       Flavor
	q       func(string) string
	cur     *driver.TableDetail
	cols    []column
	pk      *[]string
	idx     []index
	fks     []fkey
	checks  []check
	trigs   []string
	comment *string
	rename  string
}

// Build validates a change set and returns its statements in execution
// order. Errors are *ChangeError.
func Build(in Input) ([]Statement, error) {
	p, err := validate(in)
	if err != nil {
		return nil, err
	}
	switch p.f {
	case Postgres:
		return p.postgres()
	case MySQL, MariaDB:
		return p.mysql()
	default:
		return p.sqlite()
	}
}

func (p *plan) table() string { return p.q(p.in.Change.Schema) + "." + p.q(p.tableName()) }

func (p *plan) tableName() string {
	if p.in.Change.Create {
		return p.in.Change.Table
	}
	return p.cur.Name
}

func validate(in Input) (*plan, error) {
	tc := in.Change
	f := Flavor(in.Caps.Flavor)
	p := &plan{in: in, f: f, q: in.Dialect.QuoteIdent, cur: in.Current}
	create := tc.Create
	if create {
		if ce := checkName(in.Caps, SectionTable, -1, "name", tc.Table); ce != nil {
			return nil, ce
		}
		if len(tc.Columns) == 0 {
			return nil, invalid(SectionColumns, -1, "", "A table needs at least one column.")
		}
		if len(tc.DropTriggers) > 0 || tc.NewName != nil {
			return nil, invalid(SectionTable, -1, "", "A new table has nothing to drop or rename.")
		}
		p.cur = driver.NewTableDetail(driver.Table{Schema: tc.Schema, Name: tc.Table, Type: driver.TypeTable})
	} else {
		if in.Current == nil {
			return nil, notFound(SectionTable, -1, "", "The table was not found.")
		}
		if in.Current.Type != driver.TypeTable {
			return nil, unsupported(SectionTable, -1, "", fmt.Sprintf("%s is a %s; only tables can be changed.", in.Current.Name, in.Current.Type))
		}
		if tc.Empty() {
			return nil, invalid(SectionTable, -1, "", "There are no changes.")
		}
	}
	if err := p.validateColumns(); err != nil {
		return nil, err
	}
	final := p.finalColumns()
	if tc.PrimaryKey != nil {
		if !create && !in.Caps.PrimaryKey {
			return nil, unsupported(SectionPK, -1, "", in.Caps.Reason)
		}
		if ce := checkColumns(SectionPK, -1, "columns", *tc.PrimaryKey, final, create || len(*tc.PrimaryKey) > 0); ce != nil {
			return nil, ce
		}
		p.pk = tc.PrimaryKey
	}
	if err := p.validateIndexes(final); err != nil {
		return nil, err
	}
	if err := p.validateFKs(final); err != nil {
		return nil, err
	}
	if err := p.validateChecks(); err != nil {
		return nil, err
	}
	for i, name := range tc.DropTriggers {
		if !slices.ContainsFunc(p.cur.Triggers, func(t driver.Trigger) bool { return t.Name == name }) {
			return nil, notFound(SectionTriggers, i, "name", fmt.Sprintf("Trigger %q was not found.", name))
		}
		p.trigs = append(p.trigs, name)
	}
	if tc.Comment != nil {
		if !in.Caps.Comments {
			return nil, unsupported(SectionTable, -1, "comment", "This engine has no table comments.")
		}
		if _, err := in.Lit.quote(*tc.Comment); err != nil {
			return nil, invalid(SectionTable, -1, "comment", err.Error())
		}
		p.comment = tc.Comment
	}
	if tc.NewName != nil && *tc.NewName != p.cur.Name {
		if !in.Caps.RenameTable {
			return nil, unsupported(SectionTable, -1, "name", in.Caps.Reason)
		}
		if ce := checkName(in.Caps, SectionTable, -1, "name", *tc.NewName); ce != nil {
			return nil, ce
		}
		p.rename = *tc.NewName
	}
	return p, nil
}

func (p *plan) colDetail(name string) *driver.ColumnDetail {
	for i := range p.cur.Columns {
		if p.cur.Columns[i].Name == name {
			return &p.cur.Columns[i]
		}
	}
	return nil
}

func (p *plan) validateColumns() error {
	in, tc := p.in, p.in.Change
	seen := map[string]bool{}
	newNames := map[string]bool{}
	for i, ch := range tc.Columns {
		c := column{i: i, ch: ch, name: ch.Name}
		switch ch.Op {
		case OpAdd:
			if ce := checkName(in.Caps, SectionColumns, i, "name", ch.Name); ce != nil {
				return ce
			}
			if ch.Type == nil {
				return invalid(SectionColumns, i, "type", "A type is required.")
			}
			if ch.Using != nil {
				return invalid(SectionColumns, i, "using", "USING only applies to a type change.")
			}
		case OpAlter, OpDrop:
			if tc.Create {
				return invalid(SectionColumns, i, "", "A new table only adds columns.")
			}
			c.cur = p.colDetail(ch.Name)
			if c.cur == nil {
				return notFound(SectionColumns, i, "name", fmt.Sprintf("Column %q was not found.", ch.Name))
			}
			if seen[ch.Name] {
				return invalid(SectionColumns, i, "name", fmt.Sprintf("Column %q is changed twice.", ch.Name))
			}
			seen[ch.Name] = true
			if ch.Op == OpDrop {
				if !in.Caps.DropColumn {
					return unsupported(SectionColumns, i, "", "Dropping columns needs SQLite 3.35 or later.")
				}
				p.cols = append(p.cols, c)
				continue
			}
			if ch.Generated != nil {
				return invalid(SectionColumns, i, "generated", "A column cannot be made generated; add a new one.")
			}
			if ch.NewName != "" && ch.NewName != ch.Name {
				if !in.Caps.RenameColumn {
					return unsupported(SectionColumns, i, "name", "Renaming columns needs SQLite 3.25 or later.")
				}
				if ce := checkName(in.Caps, SectionColumns, i, "name", ch.NewName); ce != nil {
					return ce
				}
				c.name = ch.NewName
			}
			attrs := ch.Type != nil || ch.Nullable != nil || ch.Default != nil || ch.AutoIncrement != nil
			if attrs && !in.Caps.AlterColumn {
				return unsupported(SectionColumns, i, "", in.Caps.Reason)
			}
			if ch.Using != nil && (!in.Caps.UsingExpr || ch.Type == nil) {
				return invalid(SectionColumns, i, "using", "USING needs a type change (PostgreSQL).")
			}
		default:
			return invalid(SectionColumns, i, "", fmt.Sprintf("Unknown column operation %q.", ch.Op))
		}
		if ch.Comment != nil && !in.Caps.Comments {
			return unsupported(SectionColumns, i, "comment", "This engine has no column comments.")
		}
		if ch.Comment != nil {
			if _, err := in.Lit.quote(*ch.Comment); err != nil {
				return invalid(SectionColumns, i, "comment", err.Error())
			}
		}
		if ch.Collation != nil && !in.Caps.Collation {
			return unsupported(SectionColumns, i, "collation", "Column collations can only be set on MySQL and MariaDB.")
		}
		if ch.Collation != nil && *ch.Collation != "" && !validCollation(*ch.Collation) {
			return invalid(SectionColumns, i, "collation", fmt.Sprintf("%q is not a collation name.", *ch.Collation))
		}
		if ch.Type != nil {
			t, ce := checkType(p.f, in.Lit, SectionColumns, i, ch.Type)
			if ce != nil {
				return ce
			}
			c.typ = t
		}
		if ch.Default != nil {
			d, ce := defaultSQL(in.Kind, in.Lit, SectionColumns, i, ch.Default)
			if ce != nil {
				return ce
			}
			c.def, c.defSet = d, true
		}
		if ch.Using != nil {
			u, ce := checkExpr(in.Kind, SectionColumns, i, "using", *ch.Using)
			if ce != nil {
				return ce
			}
			c.using = u
		}
		if ch.Generated != nil {
			if !tc.Create && ((ch.Generated.Stored && !in.Caps.GeneratedStored) || (!ch.Generated.Stored && !in.Caps.GeneratedVirtual)) {
				return unsupported(SectionColumns, i, "generated", "This kind of generated column cannot be added to an existing table here.")
			}
			if p.f == Postgres && !ch.Generated.Stored {
				return unsupported(SectionColumns, i, "generated", "PostgreSQL only has stored generated columns.")
			}
			if ch.Default != nil && ch.Default.Kind != DefaultNone && ch.Default.Kind != "" {
				return invalid(SectionColumns, i, "default", "A generated column has no default.")
			}
			g, ce := checkExpr(in.Kind, SectionColumns, i, "generated", ch.Generated.Expr)
			if ce != nil {
				return ce
			}
			c.gen = g
		}
		if c.name != ch.Name || ch.Op == OpAdd {
			if newNames[c.name] {
				return invalid(SectionColumns, i, "name", fmt.Sprintf("Two columns are named %q.", c.name))
			}
			newNames[c.name] = true
		}
		p.cols = append(p.cols, c)
	}
	// A new or renamed name must not clash with a column that stays.
	for _, c := range p.cols {
		if c.ch.Op == OpDrop || (c.ch.Op == OpAlter && c.name == c.ch.Name) {
			continue
		}
		for _, cur := range p.cur.Columns {
			if cur.Name == c.name && !p.columnLeaves(cur.Name) {
				return invalid(SectionColumns, c.i, "name", fmt.Sprintf("A column named %q already exists.", c.name))
			}
		}
	}
	if !tc.Create && len(p.finalColumns()) == 0 {
		return invalid(SectionColumns, -1, "", "A table needs at least one column.")
	}
	if p.f == SQLite && !tc.Create {
		if err := p.sqliteAddRules(); err != nil {
			return err
		}
	}
	return nil
}

// validCollation: collation names are letters, digits and underscores.
func validCollation(s string) bool {
	for _, r := range s {
		if !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return s != ""
}

// hasPK reports whether the table has a primary key now.
func (p *plan) hasPK() bool {
	return slices.ContainsFunc(p.cur.Columns, func(c driver.ColumnDetail) bool { return c.IsPrimaryKey })
}

// columnLeaves reports a current column that is dropped or renamed.
func (p *plan) columnLeaves(name string) bool {
	for _, c := range p.cols {
		if c.ch.Name == name && (c.ch.Op == OpDrop || (c.ch.Op == OpAlter && c.name != name)) {
			return true
		}
	}
	return false
}

// finalColumns are the column names after the change.
func (p *plan) finalColumns() map[string]bool {
	out := map[string]bool{}
	for _, cur := range p.cur.Columns {
		if !p.columnLeaves(cur.Name) {
			out[cur.Name] = true
		}
	}
	for _, c := range p.cols {
		if c.ch.Op == OpAdd || (c.ch.Op == OpAlter && c.name != c.ch.Name) {
			out[c.name] = true
		}
	}
	return out
}

// checkColumns validates a column list against the final columns.
func checkColumns(section string, index int, field string, cols []string, final map[string]bool, required bool) *ChangeError {
	if required && len(cols) == 0 {
		return invalid(section, index, field, "Choose at least one column.")
	}
	seen := map[string]bool{}
	for _, c := range cols {
		if !final[c] {
			return notFound(section, index, field, fmt.Sprintf("Column %q was not found.", c))
		}
		if seen[c] {
			return invalid(section, index, field, fmt.Sprintf("Column %q is listed twice.", c))
		}
		seen[c] = true
	}
	return nil
}

func (p *plan) validateIndexes(final map[string]bool) error {
	in, tc := p.in, p.in.Change
	names := map[string]bool{}
	for _, ix := range p.cur.Indexes {
		names[ix.Name] = true
	}
	for i, ch := range tc.Indexes {
		x := index{i: i, ch: ch}
		cur := slices.IndexFunc(p.cur.Indexes, func(ix driver.Index) bool { return ix.Name == ch.Name })
		switch ch.Op {
		case OpDrop, OpRename:
			if tc.Create {
				return invalid(SectionIndexes, i, "", "A new table only adds indexes.")
			}
			if cur < 0 {
				return notFound(SectionIndexes, i, "name", fmt.Sprintf("Index %q was not found.", ch.Name))
			}
			if p.cur.Indexes[cur].Primary {
				return invalid(SectionIndexes, i, "name", "Change the primary key in the Primary key section.")
			}
			delete(names, ch.Name)
			if ch.Op == OpRename {
				if !in.Caps.RenameIndex {
					return unsupported(SectionIndexes, i, "name", "Renaming indexes is not supported by this server version.")
				}
				if ce := checkName(in.Caps, SectionIndexes, i, "name", ch.NewName); ce != nil {
					return ce
				}
				if names[ch.NewName] {
					return invalid(SectionIndexes, i, "name", fmt.Sprintf("An index named %q already exists.", ch.NewName))
				}
				names[ch.NewName] = true
			}
			p.idx = append(p.idx, x)
			continue
		case OpAdd:
		default:
			return invalid(SectionIndexes, i, "", fmt.Sprintf("Unknown index operation %q.", ch.Op))
		}
		if ce := checkName(in.Caps, SectionIndexes, i, "name", ch.Name); ce != nil {
			return ce
		}
		if names[ch.Name] {
			return invalid(SectionIndexes, i, "name", fmt.Sprintf("An index named %q already exists.", ch.Name))
		}
		names[ch.Name] = true
		if len(ch.Columns) == 0 {
			return invalid(SectionIndexes, i, "columns", "Choose at least one column.")
		}
		method := ""
		if ch.Method != "" {
			k := slices.IndexFunc(in.Caps.IndexMethods, func(m string) bool { return strings.EqualFold(m, ch.Method) })
			if k < 0 {
				return invalid(SectionIndexes, i, "method", fmt.Sprintf("%q is not an index method here.", ch.Method))
			}
			method = in.Caps.IndexMethods[k]
		}
		x.ch.Method = method
		if ch.Unique && (method == "FULLTEXT" || method == "SPATIAL") {
			return invalid(SectionIndexes, i, "unique", fmt.Sprintf("A %s index cannot be unique.", method))
		}
		for _, k := range ch.Columns {
			var part string
			switch {
			case k.Column != "" && k.Expr != "":
				return invalid(SectionIndexes, i, "columns", "A key part is a column or an expression, not both.")
			case k.Column != "":
				if !final[k.Column] {
					return notFound(SectionIndexes, i, "columns", fmt.Sprintf("Column %q was not found.", k.Column))
				}
				part = p.q(k.Column)
			default:
				if !in.Caps.ExpressionIndexes {
					return unsupported(SectionIndexes, i, "columns", "Expression indexes are not supported by this server version.")
				}
				e, ce := checkExpr(in.Kind, SectionIndexes, i, "columns", k.Expr)
				if ce != nil {
					return ce
				}
				part = "(" + e + ")"
			}
			if k.Length != 0 {
				if !in.Caps.IndexPrefix || k.Column == "" || k.Length < 0 {
					return invalid(SectionIndexes, i, "columns", "Prefix lengths are for MySQL and MariaDB columns only.")
				}
				part += fmt.Sprintf("(%d)", k.Length)
			}
			if k.Desc {
				part += " DESC"
			}
			x.cols = append(x.cols, part)
		}
		if strings.TrimSpace(ch.Predicate) != "" {
			if !in.Caps.PartialIndexes {
				return unsupported(SectionIndexes, i, "predicate", "Partial indexes are for PostgreSQL and SQLite.")
			}
			pr, ce := checkExpr(in.Kind, SectionIndexes, i, "predicate", ch.Predicate)
			if ce != nil {
				return ce
			}
			x.pred = pr
		}
		p.idx = append(p.idx, x)
	}
	return nil
}

func (p *plan) validateFKs(final map[string]bool) error {
	in, tc := p.in, p.in.Change
	for i, ch := range tc.ForeignKeys {
		if !tc.Create && !in.Caps.ForeignKeys {
			return unsupported(SectionFKs, i, "", in.Caps.Reason)
		}
		x := fkey{i: i, ch: ch}
		switch ch.Op {
		case OpDrop:
			if tc.Create {
				return invalid(SectionFKs, i, "", "A new table only adds foreign keys.")
			}
			if !slices.ContainsFunc(p.cur.ForeignKeys, func(fk driver.ForeignKey) bool { return fk.Name == ch.Name }) {
				return notFound(SectionFKs, i, "name", fmt.Sprintf("Foreign key %q was not found.", ch.Name))
			}
			p.fks = append(p.fks, x)
			continue
		case OpAdd:
		default:
			return invalid(SectionFKs, i, "", fmt.Sprintf("Unknown foreign key operation %q.", ch.Op))
		}
		if ch.Name != "" {
			if ce := checkName(in.Caps, SectionFKs, i, "name", ch.Name); ce != nil {
				return ce
			}
		}
		if ce := checkColumns(SectionFKs, i, "columns", ch.Columns, final, true); ce != nil {
			return ce
		}
		if strings.TrimSpace(ch.RefTable) == "" {
			return invalid(SectionFKs, i, "refTable", "Choose the referenced table.")
		}
		x.refSchema = ch.RefSchema
		if x.refSchema == "" {
			x.refSchema = tc.Schema
		}
		if p.f == SQLite && x.refSchema != tc.Schema {
			return unsupported(SectionFKs, i, "refTable", "SQLite foreign keys reference tables of the same database.")
		}
		if len(ch.RefColumns) != len(ch.Columns) {
			return invalid(SectionFKs, i, "refColumns", "Choose one referenced column per column.")
		}
		self := x.refSchema == tc.Schema && (ch.RefTable == p.tableName() || ch.RefTable == p.rename)
		if self {
			if ce := checkColumns(SectionFKs, i, "refColumns", ch.RefColumns, final, true); ce != nil {
				return ce
			}
		} else if in.Refs != nil {
			cols, err := in.Refs(x.refSchema, ch.RefTable)
			if err != nil {
				return notFound(SectionFKs, i, "refTable", fmt.Sprintf("Table %s.%s was not found.", x.refSchema, ch.RefTable))
			}
			set := map[string]bool{}
			for _, c := range cols {
				set[c] = true
			}
			if ce := checkColumns(SectionFKs, i, "refColumns", ch.RefColumns, set, true); ce != nil {
				return ce
			}
		}
		var ce *ChangeError
		if x.onUpdate, ce = checkAction(SectionFKs, i, "onUpdate", ch.OnUpdate); ce != nil {
			return ce
		}
		if x.onDelete, ce = checkAction(SectionFKs, i, "onDelete", ch.OnDelete); ce != nil {
			return ce
		}
		p.fks = append(p.fks, x)
	}
	return nil
}

func (p *plan) validateChecks() error {
	in, tc := p.in, p.in.Change
	for i, ch := range tc.Checks {
		if !in.Caps.Checks && !(tc.Create && p.f == SQLite) {
			reason := in.Caps.Reason
			if reason == "" {
				reason = "Check constraints are not supported here."
			}
			return unsupported(SectionChecks, i, "", reason)
		}
		x := check{i: i, ch: ch}
		switch ch.Op {
		case OpDrop:
			if tc.Create {
				return invalid(SectionChecks, i, "", "A new table only adds checks.")
			}
			if !slices.ContainsFunc(p.cur.Checks, func(c driver.Check) bool { return c.Name == ch.Name }) {
				return notFound(SectionChecks, i, "name", fmt.Sprintf("Check %q was not found.", ch.Name))
			}
		case OpAdd:
			if ch.Name != "" {
				if ce := checkName(in.Caps, SectionChecks, i, "name", ch.Name); ce != nil {
					return ce
				}
			}
			e, ce := checkExpr(in.Kind, SectionChecks, i, "expression", ch.Expression)
			if ce != nil {
				return ce
			}
			x.expr = e
		default:
			return invalid(SectionChecks, i, "", fmt.Sprintf("Unknown check operation %q.", ch.Op))
		}
		p.checks = append(p.checks, x)
	}
	return nil
}

// sqliteAddRules applies SQLite's restrictions on ALTER TABLE ADD COLUMN.
func (p *plan) sqliteAddRules() error {
	for _, c := range p.cols {
		if c.ch.Op != OpAdd {
			continue
		}
		if c.ch.AutoIncrement != nil && *c.ch.AutoIncrement {
			return unsupported(SectionColumns, c.i, "autoIncrement", "SQLite cannot add an auto-increment column to an existing table.")
		}
		if c.ch.Generated != nil && c.ch.Generated.Stored {
			return unsupported(SectionColumns, c.i, "generated", "SQLite can only add VIRTUAL generated columns.")
		}
		if c.defSet && c.ch.Default.Kind == DefaultExpression {
			return unsupported(SectionColumns, c.i, "default", "SQLite can only add a column with a constant default.")
		}
		notNull := c.ch.Nullable != nil && !*c.ch.Nullable
		if notNull && c.gen == "" && (!c.defSet || c.def == "" || c.def == "NULL") {
			return invalid(SectionColumns, c.i, "default", "SQLite needs a default to add a NOT NULL column.")
		}
	}
	return nil
}

// fkClause renders FOREIGN KEY … REFERENCES ….
func (p *plan) fkClause(x fkey) string {
	var b strings.Builder
	if x.ch.Name != "" {
		b.WriteString("CONSTRAINT " + p.q(x.ch.Name) + " ")
	}
	b.WriteString("FOREIGN KEY (" + p.list(x.ch.Columns) + ") REFERENCES ")
	if p.f == SQLite {
		b.WriteString(p.q(x.ch.RefTable))
	} else {
		b.WriteString(p.q(x.refSchema) + "." + p.q(x.ch.RefTable))
	}
	b.WriteString(" (" + p.list(x.ch.RefColumns) + ")")
	if x.onUpdate != "" {
		b.WriteString(" ON UPDATE " + x.onUpdate)
	}
	if x.onDelete != "" {
		b.WriteString(" ON DELETE " + x.onDelete)
	}
	return b.String()
}

func (p *plan) checkClause(x check) string {
	s := "CHECK (" + x.expr + ")"
	if x.ch.Name != "" {
		s = "CONSTRAINT " + p.q(x.ch.Name) + " " + s
	}
	return s
}

func (p *plan) list(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = p.q(n)
	}
	return strings.Join(out, ", ")
}

func (p *plan) lit(s string) string {
	q, _ := p.in.Lit.quote(s) // validated
	return q
}

func stmt(section string, index int, sql string) Statement {
	return Statement{SQL: sql, Section: section, Index: index}
}
