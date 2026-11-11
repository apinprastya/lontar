package query

import (
	"strings"

	dbdriver "dbedit/internal/driver"
	"dbedit/internal/sqlsplit"
)

// readKeywords start statements that never change data or schema: queries,
// metadata commands and transaction control (a COMMIT only makes earlier,
// already confirmed writes permanent).
var readKeywords = map[string]bool{
	"SELECT": true, "WITH": true, "VALUES": true, "TABLE": true, "SHOW": true,
	"EXPLAIN": true, "DESCRIBE": true, "DESC": true, "HELP": true, "PRAGMA": true,
	"FETCH": true, "BEGIN": true, "START": true, "COMMIT": true, "END": true,
	"ROLLBACK": true, "SAVEPOINT": true, "RELEASE": true, "USE": true,
}

// dataChangeWords make a WITH or EXPLAIN (ANALYZE) statement a write.
var dataChangeWords = map[string]bool{"INSERT": true, "UPDATE": true, "DELETE": true, "MERGE": true, "REPLACE": true}

// IsWrite classifies one statement by its leading keyword (the same word
// scan the editor uses). It is deliberately conservative: anything that is
// not a known read is a write, including SET, CALL/EXECUTE/DO and DDL, so
// that "confirm" safe mode asks before running it. Known limits: a SELECT
// that calls a function with side effects (nextval, pg_terminate_backend,
// a user function) counts as a read.
func IsWrite(kind dbdriver.Kind, text string) bool {
	words := sqlsplit.Words(kind, text, 0)
	if len(words) == 0 {
		return false
	}
	first := words[0]
	if !readKeywords[first] {
		return true
	}
	switch first {
	case "SELECT":
		// SELECT … INTO creates a table (PostgreSQL) or writes a file
		// (MySQL INTO OUTFILE).
		return containsWord(words, "INTO")
	case "WITH":
		return containsAny(words[1:], dataChangeWords)
	case "EXPLAIN":
		// EXPLAIN ANALYZE runs the statement.
		return containsWord(words, "ANALYZE") && containsAny(words, dataChangeWords)
	case "START":
		// START TRANSACTION; START SLAVE/REPLICA … changes server state.
		return len(words) < 2 || words[1] != "TRANSACTION"
	case "PRAGMA":
		// PRAGMA x = y changes a setting; PRAGMA x / x(arg) reads one.
		return strings.Contains(text, "=")
	}
	return false
}

// WriteStatements returns the statements of script that IsWrite flags.
func WriteStatements(kind dbdriver.Kind, script string) []string {
	out := []string{}
	for _, st := range sqlsplit.Split(kind, script) {
		if IsWrite(kind, st.Text) {
			out = append(out, st.Text)
		}
	}
	return out
}

func containsWord(words []string, w string) bool {
	for _, x := range words {
		if x == w {
			return true
		}
	}
	return false
}

func containsAny(words []string, set map[string]bool) bool {
	for _, x := range words {
		if set[x] {
			return true
		}
	}
	return false
}
