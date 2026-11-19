package mcpserver

import (
	"errors"
	"fmt"
	"strings"

	"lontar/internal/driver"
	"lontar/internal/query"
	"lontar/internal/sqlsplit"
)

// blockedReadWords start statements run_query refuses although
// query.IsWrite counts them as reads: transaction control would end the
// read-only transaction the statements run in, and USE would leave a
// pooled MySQL connection on another database.
var blockedReadWords = map[string]bool{
	"BEGIN": true, "START": true, "COMMIT": true, "END": true, "ROLLBACK": true,
	"SAVEPOINT": true, "RELEASE": true, "USE": true,
}

// sideEffectFunctions are functions that act outside the transaction (or
// read server files) even in a read-only one. run_query refuses statements
// that mention them; the read-only session and transaction stop the rest.
var sideEffectFunctions = map[string]bool{
	// PostgreSQL
	"PG_TERMINATE_BACKEND": true, "PG_CANCEL_BACKEND": true, "PG_RELOAD_CONF": true,
	"PG_ROTATE_LOGFILE": true, "PG_PROMOTE": true, "PG_SWITCH_WAL": true,
	"PG_CREATE_RESTORE_POINT": true, "PG_LOG_BACKEND_MEMORY_CONTEXTS": true,
	"PG_READ_FILE": true, "PG_READ_BINARY_FILE": true, "PG_LS_DIR": true, "PG_STAT_FILE": true,
	"LO_IMPORT": true, "LO_EXPORT": true, "SET_CONFIG": true, "NEXTVAL": true, "SETVAL": true,
	"DBLINK": true, "DBLINK_EXEC": true, "DBLINK_CONNECT": true, "DBLINK_SEND_QUERY": true,
	// MySQL/MariaDB
	"LOAD_FILE": true,
	// SQLite
	"LOAD_EXTENSION": true,
}

// writeWords start the statements execute_write accepts (data changes
// only; WITH is accepted when query.IsWrite finds a data change in it).
var writeWords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "REPLACE": true, "WITH": true,
}

// errNoStatements is returned for SQL without statements.
var errNoStatements = errors.New("sql has no statements")

// splitStatements splits sql into statements, refusing psql
// meta-commands (they are not SQL).
func splitStatements(kind driver.Kind, sql string) ([]string, error) {
	stmts := sqlsplit.Split(kind, sql)
	if len(stmts) == 0 {
		return nil, errNoStatements
	}
	out := make([]string, 0, len(stmts))
	for i, st := range stmts {
		if st.Meta {
			return nil, fmt.Errorf("statement %d is a psql meta-command (%s), which is not SQL", i+1, firstLine(st.Text))
		}
		out = append(out, st.Text)
	}
	return out, nil
}

// checkRead returns why run_query refuses statement i (1-based), or nil.
func checkRead(kind driver.Kind, i int, text string) error {
	words := sqlsplit.Words(kind, text, 0)
	if len(words) == 0 {
		return nil
	}
	if query.IsWrite(kind, text) {
		return fmt.Errorf("statement %d (%s) changes data or settings; run_query is read-only. Data changes go through execute_write, where the connection allows them", i, words[0])
	}
	if blockedReadWords[words[0]] {
		return fmt.Errorf("statement %d starts with %s, which run_query does not allow: every call already runs in its own read-only transaction; to query another database pass the database argument", i, words[0])
	}
	for _, w := range words {
		if sideEffectFunctions[w] {
			return fmt.Errorf("statement %d calls %s, which has effects outside a read-only transaction; run_query refuses it", i, strings.ToLower(w))
		}
	}
	return nil
}

// checkWrite returns why execute_write refuses statement i (1-based).
func checkWrite(kind driver.Kind, i int, text string) error {
	words := sqlsplit.Words(kind, text, 0)
	if len(words) == 0 {
		return fmt.Errorf("statement %d is empty", i)
	}
	if !writeWords[words[0]] || !query.IsWrite(kind, text) {
		return fmt.Errorf("statement %d starts with %s; execute_write only runs INSERT, UPDATE, DELETE, MERGE and REPLACE (no DDL, no reads: use run_query for those)", i, words[0])
	}
	for _, w := range words {
		if sideEffectFunctions[w] && w != "NEXTVAL" && w != "SETVAL" {
			return fmt.Errorf("statement %d calls %s, which execute_write refuses", i, strings.ToLower(w))
		}
	}
	return nil
}

// firstLine returns s up to its first line break.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
