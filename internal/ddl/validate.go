package ddl

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"lontar/internal/driver"
	"lontar/internal/sqlsplit"
)

// checkName validates a new identifier.
func checkName(caps Caps, section string, index int, field, name string) *ChangeError {
	switch {
	case strings.TrimSpace(name) == "":
		return invalid(section, index, field, "A name is required.")
	case strings.ContainsRune(name, 0):
		return invalid(section, index, field, "Names cannot contain NUL characters.")
	case !utf8.ValidString(name):
		return invalid(section, index, field, "Names must be valid UTF-8.")
	case caps.MaxNameLength > 0 && Flavor(caps.Flavor) == Postgres && len(name) > caps.MaxNameLength:
		return invalid(section, index, field, fmt.Sprintf("%q is longer than PostgreSQL's limit of %d bytes.", name, caps.MaxNameLength))
	case caps.MaxNameLength > 0 && Flavor(caps.Flavor) != Postgres && utf8.RuneCountInString(name) > caps.MaxNameLength:
		return invalid(section, index, field, fmt.Sprintf("%q is longer than the limit of %d characters.", name, caps.MaxNameLength))
	}
	return nil
}

// typeRe: words, numbers, spaces, dots, commas, parentheses, brackets and
// double-quoted identifiers (PostgreSQL "char", schema-qualified types).
var typeRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_ .,()\[\]"]*$`)

// checkType validates a column type and returns its SQL. enum/set values
// are quoted as literals.
func checkType(f Flavor, lit Literals, section string, index int, t *TypeSpec) (string, *ChangeError) {
	text := strings.TrimSpace(t.Text)
	if text == "" {
		return "", invalid(section, index, "type", "A type is required.")
	}
	low := strings.ToLower(text)
	if (f == MySQL || f == MariaDB) && (low == "enum" || low == "set") {
		if len(t.EnumValues) == 0 {
			return "", invalid(section, index, "type", fmt.Sprintf("Add at least one %s value.", low))
		}
		vals := make([]string, len(t.EnumValues))
		for i, v := range t.EnumValues {
			q, err := lit.quote(v)
			if err != nil {
				return "", invalid(section, index, "type", err.Error())
			}
			vals[i] = q
		}
		return low + "(" + strings.Join(vals, ",") + ")", nil
	}
	bad := !typeRe.MatchString(text) || strings.Count(text, `"`)%2 != 0 ||
		strings.Contains(text, "--") || strings.Contains(text, "/*")
	if !bad {
		depth := 0
		for _, c := range text {
			switch c {
			case '(':
				depth++
			case ')':
				depth--
			}
			if depth < 0 {
				break
			}
		}
		bad = depth != 0
	}
	if bad {
		msg := fmt.Sprintf("%q is not a valid type: use letters, digits, spaces and ( , ) [ ] only.", text)
		if strings.ContainsAny(text, "'") {
			msg += " Enter enum and set values in the value list."
		}
		return "", invalid(section, index, "type", msg)
	}
	return text, nil
}

// checkExpr validates SQL written by the user that must be exactly one
// expression: one statement without a delimiter, no comment at either end
// (a trailing line comment would swallow the rest of the DDL), and
// balanced parentheses.
func checkExpr(kind driver.Kind, section string, index int, field, text string) (string, *ChangeError) {
	expr := strings.TrimSpace(text)
	if expr == "" {
		return "", invalid(section, index, field, "An expression is required.")
	}
	stmts := sqlsplit.Split(kind, expr)
	if len(stmts) != 1 || stmts[0].Text != expr || stmts[0].DelimEnd != stmts[0].End || stmts[0].Meta {
		return "", invalid(section, index, field, "Enter one expression, without ';' or comments at its ends.")
	}
	if !sqlsplit.Balanced(kind, expr) {
		return "", invalid(section, index, field, "The parentheses of the expression are not balanced.")
	}
	return expr, nil
}

// timeFuncRe matches defaults written without parentheses on every engine.
var timeFuncRe = regexp.MustCompile(`(?i)^(current_timestamp|current_date|current_time|localtime|localtimestamp|now)(\s*\(\s*\d*\s*\))?$`)

// defaultSQL renders a default ("" for none).
func defaultSQL(kind driver.Kind, lit Literals, section string, index int, d *DefaultSpec) (string, *ChangeError) {
	switch d.Kind {
	case DefaultNone, "":
		return "", nil
	case DefaultNull:
		return "NULL", nil
	case DefaultValue:
		q, err := lit.quote(d.Text)
		if err != nil {
			return "", invalid(section, index, "default", err.Error())
		}
		return q, nil
	case DefaultExpression:
		expr, ce := checkExpr(kind, section, index, "default", d.Text)
		if ce != nil {
			return "", ce
		}
		if timeFuncRe.MatchString(expr) {
			return expr, nil
		}
		return "(" + expr + ")", nil
	}
	return "", invalid(section, index, "default", fmt.Sprintf("Unknown default kind %q.", d.Kind))
}

// Literals quotes string literals for a session.
type Literals struct {
	Flavor Flavor
	// NoBackslashEscapes: MySQL/MariaDB sql_mode NO_BACKSLASH_ESCAPES.
	NoBackslashEscapes bool
}

// quote renders s as a string literal.
func (l Literals) quote(s string) (string, error) {
	if strings.ContainsRune(s, 0) {
		return "", fmt.Errorf("text cannot contain NUL characters")
	}
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("text must be valid UTF-8")
	}
	q := strings.ReplaceAll(s, "'", "''")
	switch l.Flavor {
	case Postgres:
		// E'' escapes backslashes whatever standard_conforming_strings is.
		if strings.Contains(s, `\`) {
			return "E'" + strings.ReplaceAll(q, `\`, `\\`) + "'", nil
		}
	case MySQL, MariaDB:
		if !l.NoBackslashEscapes {
			q = strings.ReplaceAll(q, `\`, `\\`)
		}
	}
	return "'" + q + "'", nil
}

// referentialActions are the accepted ON UPDATE / ON DELETE actions.
var referentialActions = map[string]bool{"": true, "NO ACTION": true, "RESTRICT": true, "CASCADE": true, "SET NULL": true, "SET DEFAULT": true}

// checkAction validates a referential action.
func checkAction(section string, index int, field, action string) (string, *ChangeError) {
	a := strings.ToUpper(strings.Join(strings.Fields(action), " "))
	if !referentialActions[a] {
		return "", invalid(section, index, field, fmt.Sprintf("%q is not a referential action.", action))
	}
	return a, nil
}
