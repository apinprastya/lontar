package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"lontar/internal/conn"
	"lontar/internal/values"
)

// resultSet is one statement's outcome in run_query.
type resultSet struct {
	columns []string
	rows    [][]values.Cell
	// more reports rows beyond maxRows (not read).
	more bool
}

// runRead runs each statement in its own READ ONLY transaction on the
// read-only session and always rolls it back. It stops at the first error,
// returning the sets read so far and the failing statement's index.
func runRead(ctx context.Context, sess *conn.Session, stmts []string, maxRows int) ([]resultSet, int, error) {
	out := make([]resultSet, 0, len(stmts))
	for i, text := range stmts {
		rs, err := readOne(ctx, sess.DB, text, maxRows)
		if err != nil {
			return out, i, err
		}
		out = append(out, rs)
	}
	return out, -1, nil
}

func readOne(ctx context.Context, db *sql.DB, text string, maxRows int) (rs resultSet, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rs, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, text)
	if err != nil {
		return rs, err
	}
	defer func() {
		if rs.more {
			// Stop the server producing the rest instead of draining it;
			// the transaction is rolled back anyway.
			cancel()
		}
		_ = rows.Close()
	}()
	types, err := rows.ColumnTypes()
	if err != nil {
		return rs, err
	}
	cats := make([]values.Category, len(types))
	rs.columns = make([]string, len(types))
	for i, t := range types {
		cats[i] = values.Categorize(t.DatabaseTypeName())
		rs.columns[i] = t.Name()
	}
	for rows.Next() {
		if len(rs.rows) == maxRows {
			rs.more = true
			break
		}
		row, err := scanRow(rows, cats)
		if err != nil {
			return rs, err
		}
		rs.rows = append(rs.rows, row)
	}
	return rs, rows.Err()
}

func scanRow(rows *sql.Rows, cats []values.Category) ([]values.Cell, error) {
	dest := make([]any, len(cats))
	ptrs := make([]any, len(cats))
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	row := make([]values.Cell, len(cats))
	for i, v := range dest {
		row[i] = values.ConvertCat(v, cats[i])
	}
	return row, nil
}

// renderResults writes the result sets as CSV blocks within the output
// budget.
func renderResults(sets []resultSet, total, maxRows int) string {
	var b strings.Builder
	bud := newBudget()
	for i, rs := range sets {
		if total > 1 {
			fmt.Fprintf(&b, "-- Statement %d of %d: ", i+1, total)
		}
		b.WriteString(summary(rs, maxRows))
		b.WriteString("\n")
		if len(rs.columns) == 0 {
			continue
		}
		header := make([]string, len(rs.columns))
		for j, c := range rs.columns {
			header[j] = csvField(c, false)
		}
		line := csvLine(header)
		if !bud.take(len(line)) {
			b.WriteString("(output limit reached)\n")
			break
		}
		b.WriteString(line)
		for j, row := range rs.rows {
			line := rowLine(row)
			if !bud.take(len(line)) {
				fmt.Fprintf(&b, "… output limit (%d KB) reached: %d more rows read but not shown. Select fewer columns, filter or aggregate.\n", MaxOutputBytes>>10, len(rs.rows)-j)
				break
			}
			b.WriteString(line)
		}
		if bud.hit {
			break
		}
	}
	return b.String()
}

// summary describes one result set's size.
func summary(rs resultSet, maxRows int) string {
	if len(rs.columns) == 0 {
		return "no result set"
	}
	n := int64(len(rs.rows))
	if rs.more {
		return fmt.Sprintf("first %s shown; more rows exist (max_rows=%d, up to %d; or filter/aggregate)", plural(n, "row", "rows"), maxRows, MaxMaxRows)
	}
	return plural(n, "row", "rows")
}

// rowCount sums the rows returned.
func rowCount(sets []resultSet) int64 {
	var n int64
	for _, rs := range sets {
		n += int64(len(rs.rows))
	}
	return n
}

// elapsed returns the milliseconds since start.
func elapsed(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}
