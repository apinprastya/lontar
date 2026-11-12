package edits

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"lontar/internal/driver"
	"lontar/internal/query"
)

func TestResolveSource(t *testing.T) {
	forEach(t, func(t *testing.T, v env) {
		ctx := context.Background()
		idType := "INTEGER"
		v.exec(t,
			"CREATE TABLE src_people ("+v.q("id")+" "+idType+" PRIMARY KEY, "+v.q("Name")+" VARCHAR(40) NOT NULL, note VARCHAR(40) DEFAULT 'x')",
			"CREATE TABLE src_nokey (a INTEGER)",
			"CREATE VIEW src_view AS SELECT * FROM src_people",
		)
		resolve := func(sqlText string, n int) *Source {
			t.Helper()
			src, err := ResolveSource(ctx, v.db, v.e.drv, v.kind(), sqlText, v.schema, n)
			if err != nil {
				t.Fatalf("%s: %v", sqlText, err)
			}
			return src
		}
		name := "Name"
		if v.kind() == driver.Postgres {
			name = `"Name"`
		}

		src := resolve("SELECT * FROM src_people", 3)
		if !src.Editable || src.Table != "src_people" || src.Schema != v.schema {
			t.Fatalf("star: %+v", src)
		}
		if c := src.Columns[0]; c.Column != "id" || !c.IsPK {
			t.Errorf("id column: %+v", c)
		}
		if c := src.Columns[2]; c.Column != "note" || !c.HasDefault || !c.Nullable {
			t.Errorf("note column: %+v", c)
		}

		// Expressions and repeats stay read-only; aliases keep the column.
		src = resolve("SELECT id, "+name+" AS who, upper(note), id FROM src_people p WHERE id > 0", 4)
		if !src.Editable {
			t.Fatalf("columns: %+v", src)
		}
		got := []string{}
		for _, c := range src.Columns {
			got = append(got, c.Column)
		}
		if strings.Join(got, ",") != "id,Name,," {
			t.Errorf("mapping: %v", got)
		}

		// Qualified with the schema (SQLite: main).
		schema := v.schema
		if schema == "" {
			schema = "main"
		}
		if src := resolve("SELECT * FROM "+v.q(schema)+".src_people", 3); !src.Editable {
			t.Errorf("qualified: %+v", src)
		}

		for sqlText, want := range map[string]string{
			"SELECT " + name + " FROM src_people":                 "primary key (id)",
			"SELECT * FROM src_nokey":                             "no primary key",
			"SELECT * FROM src_view":                              "view",
			"SELECT * FROM src_missing":                           "not found",
			"SELECT * FROM src_people a JOIN src_nokey b ON true": "several tables",
		} {
			src := resolve(sqlText, 1)
			if src.Editable || !strings.Contains(src.Reason, want) {
				t.Errorf("%s: %+v, want reason with %q", sqlText, src, want)
			}
		}
		// The column count must match the expanded SELECT list.
		if src := resolve("SELECT * FROM src_people", 2); src.Editable || src.Reason != query.ReasonUnsupported {
			t.Errorf("count mismatch: %+v", src)
		}
		// Without a default schema an unqualified name cannot be found.
		if v.kind() != driver.SQLite {
			src, err := ResolveSource(ctx, v.db, v.e.drv, v.kind(), "SELECT * FROM src_people", "", 3)
			if err != nil || src.Editable || !strings.Contains(src.Reason, "default schema") {
				t.Errorf("no schema: %+v %v", src, err)
			}
		}
	})
}

func TestMatchName(t *testing.T) {
	names := []string{"Users", "users", "Orders"}
	at := func(i int) string { return names[i] }
	cases := []struct {
		kind    driver.Kind
		written string
		quoted  bool
		want    string
		ok      bool
	}{
		{driver.Postgres, "USERS", false, "users", true},
		{driver.Postgres, "Users", true, "Users", true},
		{driver.Postgres, "ORDERS", false, "", false},
		{driver.MySQL, "orders", false, "Orders", true},
		{driver.MySQL, "USERS", false, "", false}, // ambiguous
		{driver.SQLite, "users", true, "users", true},
	}
	for _, c := range cases {
		got, ok := matchName(c.kind, c.written, c.quoted, len(names), at)
		if got != c.want || ok != c.ok {
			t.Errorf("%s %q: got %q %v", c.kind, c.written, got, ok)
		}
	}
}

// failingEngine fails metadata calls, to check errors are returned.
type failingEngine struct{ query.Engine }

var errMeta = errors.New("metadata down")

func (failingEngine) ListTables(context.Context, *sql.DB, string) ([]driver.Table, error) {
	return nil, errMeta
}

func TestResolveSourceErrors(t *testing.T) {
	if _, err := ResolveSource(context.Background(), nil, failingEngine{}, driver.Postgres, "SELECT * FROM t", "public", 1); !errors.Is(err, errMeta) {
		t.Errorf("got %v", err)
	}
	src, err := ResolveSource(context.Background(), nil, failingEngine{}, driver.Postgres, "DELETE FROM t", "public", 1)
	if err != nil || src.Editable || src.Reason != query.ReasonNotSelect {
		t.Errorf("got %+v %v", src, err)
	}
}
