package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidateReadOnlySelect reports whether query is exactly one read-only
// T-SQL SELECT, optionally led by WITH (CTEs or XMLNAMESPACES), as every
// query_template and built-in probe query must be.  The error says why not,
// without quoting the query.
//
// This is defense in depth, not the control that keeps probes read-only: a
// lexical check cannot prove a query has no side effects (a SELECT can still
// call a function that writes, or read more than it should).  The control is
// the collector's login, which holds only read grants
// (docs/guides/database-targets.md#collector-login-permissions).  The check
// catches the mistakes and smuggled statements a review can miss.
//
// It works on tokens, after comments ("--" and nested "/* */"), string
// literals ('...' with ” escapes), bracketed identifiers ([...] with ]]
// escapes) and double-quoted identifiers are recognised, so a keyword inside
// any of them is ignored and a statement after a comment is still seen.  It
// rejects:
//
//   - more than one statement: text after a ';' other than more ';', or a
//     second SELECT at the top level that does not follow UNION, EXCEPT or
//     INTERSECT (T-SQL needs no ';' between statements);
//   - a first word other than SELECT or WITH;
//   - any of [forbiddenSQLKeywords] outside strings, comments and quoted
//     identifiers, which covers DML, DDL, EXEC, SELECT ... INTO, OPENROWSET
//     and the other ad hoc remote reads, WAITFOR, transactions and session
//     changes;
//   - a name starting with sp_ or xp_ (system and extended procedures);
//   - END without a matching CASE (END CONVERSATION and similar), and
//     unbalanced parentheses or an unterminated string, comment or quoted
//     identifier, which would let the server read the text differently.
func ValidateReadOnlySelect(query string) error {
	tokens, err := sqlTokens(query)
	if err != nil {
		return err
	}
	// Leading and trailing ';' are empty statements, as in ";WITH ...;".
	statement := trimSemicolons(tokens)
	if len(statement) == 0 {
		return errors.New("is empty")
	}
	if first := statement[0]; first.kind != sqlWord || (first.text != "SELECT" && first.text != "WITH") {
		return errors.New("must start with SELECT or WITH")
	}
	depth, cases, selects := 0, 0, 0
	for i, token := range statement {
		switch token.kind {
		case sqlPunct:
			switch token.text {
			case ";":
				return errors.New("has more than one statement")
			case "(":
				depth++
			case ")":
				if depth--; depth < 0 {
					return errors.New("has unbalanced parentheses")
				}
			}
		case sqlWord:
			if routinePrefix(token.text) {
				return errors.New("calls a system or extended stored procedure (sp_ or xp_ name)")
			}
			if token.afterDot {
				// A column or object name, such as r.status: never a keyword.
				continue
			}
			if reason, ok := forbiddenSQLKeywords[token.text]; ok {
				return fmt.Errorf("contains %s, %s", token.text, reason)
			}
			switch token.text {
			case "CASE":
				cases++
			case "END":
				if cases--; cases < 0 {
					return errors.New("contains END without CASE, which starts another statement")
				}
			case "SELECT":
				if depth > 0 {
					continue
				}
				if selects > 0 && !followsSetOperator(statement[:i]) {
					return errors.New("has more than one statement (a second top-level SELECT)")
				}
				selects++
			}
		}
	}
	if depth != 0 {
		return errors.New("has unbalanced parentheses")
	}
	if selects == 0 {
		return errors.New("has no top-level SELECT after WITH")
	}
	return nil
}

// forbiddenSQLKeywords maps each keyword a probe query may not contain to why.
// They are T-SQL statement keywords, all reserved words except RECEIVE and
// SEND, so none can appear unquoted as a column or table name; a quoted name
// such as [Backup] is fine.  FETCH (OFFSET ... FETCH) and END (CASE ... END)
// are valid in a SELECT and are not listed.
var forbiddenSQLKeywords = func() map[string]string {
	reasons := map[string][]string{
		"which changes data":                                    {"INSERT", "UPDATE", "DELETE", "MERGE", "TRUNCATE", "BULK", "WRITETEXT", "UPDATETEXT"},
		"which changes the schema or permissions":               {"CREATE", "ALTER", "DROP", "GRANT", "DENY", "REVOKE"},
		"which runs a procedure or dynamic SQL":                 {"EXEC", "EXECUTE"},
		"which writes the result to a table (SELECT ... INTO)":  {"INTO"},
		"which reads another server or a file":                  {"OPENROWSET", "OPENQUERY", "OPENDATASOURCE"},
		"which administers the server":                          {"SHUTDOWN", "DBCC", "BACKUP", "RESTORE", "KILL", "RECONFIGURE", "CHECKPOINT", "DISK", "LOAD", "DUMP"},
		"which holds the session":                               {"WAITFOR"},
		"which switches the database, user or session settings": {"USE", "SET", "SETUSER", "REVERT"},
		"which opens or ends a transaction":                     {"BEGIN", "COMMIT", "ROLLBACK", "SAVE", "TRAN", "TRANSACTION"},
		"which starts another statement":                        {"DECLARE", "IF", "WHILE", "GOTO", "RETURN", "BREAK", "CONTINUE", "PRINT", "RAISERROR", "READTEXT", "OPEN", "CLOSE", "DEALLOCATE", "RECEIVE", "SEND"},
	}
	out := map[string]string{}
	for reason, keywords := range reasons {
		for _, keyword := range keywords {
			out[keyword] = reason
		}
	}
	return out
}()

// routinePrefix reports whether word, upper-cased, names a system (sp_) or
// extended (xp_) stored procedure.
func routinePrefix(word string) bool {
	return strings.HasPrefix(word, "SP_") || strings.HasPrefix(word, "XP_")
}

// followsSetOperator reports whether the tokens before a SELECT end with
// UNION [ALL], EXCEPT or INTERSECT, so the SELECT continues the query.
func followsSetOperator(before []sqlToken) bool {
	if len(before) == 0 {
		return false
	}
	last := before[len(before)-1]
	if last.kind != sqlWord {
		return false
	}
	switch last.text {
	case "UNION", "EXCEPT", "INTERSECT":
		return true
	case "ALL":
		return len(before) > 1 && before[len(before)-2].kind == sqlWord && before[len(before)-2].text == "UNION"
	}
	return false
}

// trimSemicolons returns tokens without leading and trailing ';' tokens.
func trimSemicolons(tokens []sqlToken) []sqlToken {
	isSemicolon := func(t sqlToken) bool { return t.kind == sqlPunct && t.text == ";" }
	for len(tokens) > 0 && isSemicolon(tokens[0]) {
		tokens = tokens[1:]
	}
	for len(tokens) > 0 && isSemicolon(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-1]
	}
	return tokens
}

// sqlTokenKind classifies a [sqlToken].
type sqlTokenKind int

const (
	// sqlWord is an unquoted keyword or name, upper-cased.
	sqlWord sqlTokenKind = iota
	// sqlQuoted is a [bracketed] or "double-quoted" identifier.
	sqlQuoted
	// sqlString is a string literal.
	sqlString
	// sqlVariable is an @variable, @@function or #temp name.
	sqlVariable
	// sqlNumber is a number or binary literal.
	sqlNumber
	// sqlPunct is any other single character, such as ( ) , ; . or an
	// operator.
	sqlPunct
)

// sqlToken is one token of a T-SQL query.  Quoted identifiers and strings
// keep no text: nothing inside them is checked.
type sqlToken struct {
	kind sqlTokenKind
	text string
	// afterDot reports a word right after '.', which is the column or object
	// part of a multi-part name.
	afterDot bool
}

// sqlTokens splits query into tokens, dropping whitespace and comments.
func sqlTokens(query string) ([]sqlToken, error) {
	var tokens []sqlToken
	afterDot := func() bool {
		return len(tokens) > 0 && tokens[len(tokens)-1].kind == sqlPunct && tokens[len(tokens)-1].text == "."
	}
	for i := 0; i < len(query); {
		r, size := utf8.DecodeRuneInString(query[i:])
		switch {
		case unicode.IsSpace(r):
			i += size
		case strings.HasPrefix(query[i:], "--"):
			end := strings.IndexAny(query[i:], "\r\n")
			if end < 0 {
				return tokens, nil
			}
			i += end
		case strings.HasPrefix(query[i:], "/*"):
			end, err := blockCommentEnd(query, i)
			if err != nil {
				return nil, err
			}
			i = end
		case r == '\'':
			end, ok := quotedEnd(query, i, '\'')
			if !ok {
				return nil, errors.New("has an unterminated string literal")
			}
			tokens, i = append(tokens, sqlToken{kind: sqlString}), end
		case r == '[':
			end, ok := quotedEnd(query, i, ']')
			if !ok {
				return nil, errors.New("has an unterminated [bracketed] identifier")
			}
			tokens, i = append(tokens, sqlToken{kind: sqlQuoted}), end
		case r == '"':
			end, ok := quotedEnd(query, i, '"')
			if !ok {
				return nil, errors.New(`has an unterminated "quoted" identifier`)
			}
			tokens, i = append(tokens, sqlToken{kind: sqlQuoted}), end
		case r == '@' || r == '#':
			end := wordEnd(query, i+size)
			tokens, i = append(tokens, sqlToken{kind: sqlVariable}), end
		case r == '_' || unicode.IsLetter(r):
			end := wordEnd(query, i)
			tokens = append(tokens, sqlToken{kind: sqlWord, text: strings.ToUpper(query[i:end]), afterDot: afterDot()})
			i = end
		case unicode.IsDigit(r):
			end := wordEnd(query, i)
			tokens, i = append(tokens, sqlToken{kind: sqlNumber}), end
		default:
			tokens, i = append(tokens, sqlToken{kind: sqlPunct, text: string(r)}), i+size
		}
	}
	return tokens, nil
}

// wordEnd returns the index just past the name or number that continues at
// query[i:]: letters, digits, '_', '@', '#' and '$'.  A '.' ends it, so a
// multi-part name is several words; "1.5" is two numbers around a '.'.
func wordEnd(query string, i int) int {
	for i < len(query) {
		r, size := utf8.DecodeRuneInString(query[i:])
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("_@#$", r) {
			break
		}
		i += size
	}
	return i
}

// quotedEnd returns the index just past the literal or identifier that opens
// at query[start] and closes with closer, where a doubled closer is an
// escaped one, and false when it is not closed.
func quotedEnd(query string, start int, closer byte) (int, bool) {
	for i := start + 1; i < len(query); i++ {
		if query[i] != closer {
			continue
		}
		if i+1 < len(query) && query[i+1] == closer {
			i++
			continue
		}
		return i + 1, true
	}
	return 0, false
}

// blockCommentEnd returns the index just past the block comment that opens
// at query[start].  T-SQL block comments nest.
func blockCommentEnd(query string, start int) (int, error) {
	depth := 0
	for i := start; i+1 < len(query); {
		switch query[i : i+2] {
		case "/*":
			depth, i = depth+1, i+2
		case "*/":
			if depth, i = depth-1, i+2; depth == 0 {
				return i, nil
			}
		default:
			i++
		}
	}
	return 0, errors.New("has an unterminated /* comment */")
}
