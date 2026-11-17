package postgres

import (
	"database/sql"
	"strings"
	"testing"

	"lontar/internal/driver"
)

func TestBuildAggregateDDL(t *testing.T) {
	got := buildAggregateDDL(pgAggregate{
		QName: "s.pct", Args: "double precision ORDER BY double precision", SFunc: "ordered_set_transition", SType: "internal",
		FinalFunc: "pct_final", FinalExtra: true, CombineFunc: "comb", SortOp: ">", Kind: "h", Parallel: "s",
		Comment: "It's", InitCond: sql.NullString{String: "{}", Valid: true},
	})
	for _, want := range []string{
		"CREATE AGGREGATE s.pct(double precision ORDER BY double precision) (",
		"FINALFUNC = pct_final,\n    FINALFUNC_EXTRA", "COMBINEFUNC = comb", "INITCOND = '{}'", "SORTOP = >",
		"HYPOTHETICAL", "PARALLEL = SAFE", "COMMENT ON AGGREGATE s.pct(double precision ORDER BY double precision) IS 'It''s';",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	got = buildAggregateDDL(pgAggregate{QName: "s.cnt", SFunc: "int8inc", SType: "bigint", Parallel: "r"})
	if got != "CREATE AGGREGATE s.cnt(*) (\n    SFUNC = int8inc,\n    STYPE = bigint,\n    PARALLEL = RESTRICTED\n);\n" {
		t.Errorf("got\n%s", got)
	}
}

func TestTypeAndSequenceDDL(t *testing.T) {
	js := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	ddl, err := typeDDL(pgType{QSchema: "s", QName: "d", Kind: "d", Base: "integer", NotNull: true,
		Constraints: js(`[["nn", "NOT NULL"], ["c", "CHECK ((VALUE > 0))"]]`)})
	if err != nil || ddl != "CREATE DOMAIN s.d AS integer\n    CONSTRAINT nn NOT NULL\n    CONSTRAINT c CHECK ((VALUE > 0));\n" {
		t.Errorf("domain DDL = %q, %v", ddl, err)
	}
	ddl, err = typeDDL(pgType{QSchema: "s", QName: "e", Kind: "c", Comment: "x"})
	if err != nil || ddl != "CREATE TYPE s.e AS ();\n\nCOMMENT ON TYPE s.e IS 'x';\n" {
		t.Errorf("empty composite DDL = %q, %v", ddl, err)
	}
	for _, bad := range []pgType{{Labels: js("{")}, {Attrs: js("[1]")}, {Constraints: js("x")}} {
		if _, err := typeDDL(bad); err == nil {
			t.Errorf("typeDDL(%+v): expected error", bad)
		}
		if _, err := bad.object("s"); err == nil {
			t.Errorf("object(%+v): expected error", bad)
		}
	}
	seq := sequenceDDL(pgSequence{QSchema: "s", QName: "q", Type: "integer", Start: 1, Increment: -1, Min: -10, Max: -1, Cache: 1, Cycle: true})
	if !strings.Contains(seq, "INCREMENT BY -1") || !strings.Contains(seq, "    CYCLE;") || strings.Contains(seq, "OWNED BY") {
		t.Errorf("sequence DDL = %q", seq)
	}
	o := pgSequence{Name: "q", Type: "integer"}.object("s")
	if o.Kind != driver.ObjectSequence || o.Detail != "integer" {
		t.Errorf("sequence object = %+v", o)
	}
	r := pgRoutine{Name: "w", Kind: "w", Result: "bigint", Language: "internal", Volatility: "v", SecDef: true}.object("s")
	if r.Kind != driver.ObjectFunction || r.Properties[2].Value != "volatile" || r.Properties[3].Value != "definer" {
		t.Errorf("window function object = %+v", r)
	}
	tr := pgObjTrigger{Name: "t", Table: "x", Type: 1<<2 | 1<<6 | 1, Enabled: "D"}.object("s")
	if tr.Detail != "INSTEAD OF INSERT on x" {
		t.Errorf("trigger object = %+v", tr)
	}
}
