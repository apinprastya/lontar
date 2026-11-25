package ddl

import (
	"strings"
)

// sqliteColumnDef renders a column of CREATE TABLE or ADD COLUMN. inlinePK
// makes it the INTEGER PRIMARY KEY AUTOINCREMENT column.
func (p *plan) sqliteColumnDef(c column, inlinePK bool) string {
	parts := []string{p.q(c.name), c.typ}
	if inlinePK {
		parts = append(parts, "PRIMARY KEY AUTOINCREMENT")
	}
	if c.gen != "" {
		kind := "VIRTUAL"
		if c.ch.Generated.Stored {
			kind = "STORED"
		}
		parts = append(parts, "GENERATED ALWAYS AS ("+c.gen+") "+kind)
	}
	if c.defSet && c.def != "" {
		parts = append(parts, "DEFAULT "+c.def)
	}
	if c.ch.Nullable != nil && !*c.ch.Nullable {
		parts = append(parts, "NOT NULL")
	}
	return strings.Join(parts, " ")
}

func (p *plan) sqliteIndex(x index) string {
	var b strings.Builder
	b.WriteString("CREATE ")
	if x.ch.Unique {
		b.WriteString("UNIQUE ")
	}
	b.WriteString("INDEX " + p.q(p.in.Change.Schema) + "." + p.q(x.ch.Name) + " ON " + p.q(p.tableName()) + " (" + strings.Join(x.cols, ", ") + ")")
	if x.pred != "" {
		b.WriteString(" WHERE " + x.pred)
	}
	return b.String()
}

func (p *plan) sqlite() ([]Statement, error) {
	tc := p.in.Change
	t := p.table()
	var out []Statement
	add := func(section string, i int, sql string) { out = append(out, stmt(section, i, sql)) }

	if tc.Create {
		// AUTOINCREMENT needs the key inline: INTEGER PRIMARY KEY AUTOINCREMENT.
		inline := ""
		for _, c := range p.cols {
			if c.ch.AutoIncrement != nil && *c.ch.AutoIncrement {
				if inline != "" || p.pk == nil || len(*p.pk) != 1 || (*p.pk)[0] != c.name || !strings.EqualFold(c.typ, "INTEGER") {
					return nil, invalid(SectionColumns, c.i, "autoIncrement", "In SQLite only an INTEGER column that is the whole primary key can auto-increment.")
				}
				inline = c.name
			}
		}
		var defs []string
		for _, c := range p.cols {
			defs = append(defs, p.sqliteColumnDef(c, c.name == inline))
		}
		if p.pk != nil && len(*p.pk) > 0 && inline == "" {
			defs = append(defs, "PRIMARY KEY ("+p.list(*p.pk)+")")
		}
		for _, x := range p.fks {
			defs = append(defs, p.fkClause(x))
		}
		for _, x := range p.checks {
			defs = append(defs, p.checkClause(x))
		}
		add(SectionTable, -1, "CREATE TABLE "+t+" (\n  "+strings.Join(defs, ",\n  ")+"\n)")
		for _, x := range p.idx {
			add(SectionIndexes, x.i, p.sqliteIndex(x))
		}
		return out, nil
	}

	for _, x := range p.idx {
		if x.ch.Op == OpDrop {
			add(SectionIndexes, x.i, "DROP INDEX "+p.q(tc.Schema)+"."+p.q(x.ch.Name))
		}
	}
	for i, name := range p.trigs {
		add(SectionTriggers, i, "DROP TRIGGER "+p.q(tc.Schema)+"."+p.q(name))
	}
	for _, c := range p.cols {
		if c.ch.Op == OpDrop {
			add(SectionColumns, c.i, "ALTER TABLE "+t+" DROP COLUMN "+p.q(c.ch.Name))
		}
	}
	for _, c := range p.cols {
		if c.ch.Op == OpAlter && c.name != c.ch.Name {
			add(SectionColumns, c.i, "ALTER TABLE "+t+" RENAME COLUMN "+p.q(c.ch.Name)+" TO "+p.q(c.name))
		}
	}
	for _, c := range p.cols {
		if c.ch.Op == OpAdd {
			add(SectionColumns, c.i, "ALTER TABLE "+t+" ADD COLUMN "+p.sqliteColumnDef(c, false))
		}
	}
	for _, x := range p.idx {
		if x.ch.Op == OpAdd {
			add(SectionIndexes, x.i, p.sqliteIndex(x))
		}
	}
	if p.rename != "" {
		add(SectionTable, -1, "ALTER TABLE "+t+" RENAME TO "+p.q(p.rename))
	}
	return out, nil
}
