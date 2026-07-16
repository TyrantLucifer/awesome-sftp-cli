package migration

import (
	"fmt"
	"strings"
)

const applicationIDStatement = "PRAGMA application_id = 1095586630"

type StatementKind string

const (
	StatementApplicationID StatementKind = "application_id"
	StatementCreateTable   StatementKind = "create_table"
	StatementCreateIndex   StatementKind = "create_index"
	StatementCreateView    StatementKind = "create_view"
	StatementAlterTable    StatementKind = "alter_table"
	StatementDropTable     StatementKind = "drop_table"
	StatementDropIndex     StatementKind = "drop_index"
	StatementDropView      StatementKind = "drop_view"
	StatementInsert        StatementKind = "insert"
	StatementUpdate        StatementKind = "update"
	StatementDelete        StatementKind = "delete"
)

type AdmittedStatement struct {
	Kind   StatementKind
	Target string
}

type sqlToken struct {
	value  string
	quoted bool
}

// AdmitStatement proves that one migration slice element contains exactly one
// allowlisted main-schema statement.
func AdmitStatement(version uint64, index int, statement string) (AdmittedStatement, error) {
	if statement == applicationIDStatement {
		if version == 1 && index == 1 {
			return AdmittedStatement{Kind: StatementApplicationID}, nil
		}
		return AdmittedStatement{}, fmt.Errorf("admit SQL: application ID operation is allowed only at version 1 index 1")
	}
	tokens, err := lexSingleStatement(statement)
	if err != nil {
		return AdmittedStatement{}, err
	}
	if len(tokens) == 0 {
		return AdmittedStatement{}, fmt.Errorf("admit SQL: statement has no tokens")
	}
	keyword := folded(tokens[0])
	switch keyword {
	case "CREATE":
		return admitCreate(tokens)
	case "ALTER":
		return admitTargetAfter(tokens, StatementAlterTable, []string{"ALTER", "TABLE"}, false)
	case "DROP":
		return admitDrop(tokens)
	case "INSERT":
		return admitTargetAfter(tokens, StatementInsert, []string{"INSERT", "INTO"}, false)
	case "UPDATE":
		return admitTargetAt(tokens, StatementUpdate, 1)
	case "DELETE":
		return admitTargetAfter(tokens, StatementDelete, []string{"DELETE", "FROM"}, false)
	default:
		return AdmittedStatement{}, fmt.Errorf("admit SQL: keyword %q is not allowlisted", tokens[0].value)
	}
}

func admitCreate(tokens []sqlToken) (AdmittedStatement, error) {
	if len(tokens) < 3 {
		return AdmittedStatement{}, fmt.Errorf("admit CREATE: incomplete statement")
	}
	second := folded(tokens[1])
	switch second {
	case "TEMP", "TEMPORARY", "VIRTUAL", "TRIGGER":
		return AdmittedStatement{}, fmt.Errorf("admit CREATE: %s is forbidden", second)
	case "TABLE":
		return admitCreateObject(tokens, StatementCreateTable, 2)
	case "VIEW":
		return admitCreateObject(tokens, StatementCreateView, 2)
	case "INDEX":
		return admitCreateObject(tokens, StatementCreateIndex, 2)
	case "UNIQUE":
		if len(tokens) < 4 || folded(tokens[2]) != "INDEX" {
			return AdmittedStatement{}, fmt.Errorf("admit CREATE UNIQUE: expected INDEX")
		}
		return admitCreateObject(tokens, StatementCreateIndex, 3)
	default:
		return AdmittedStatement{}, fmt.Errorf("admit CREATE: object type %q is not allowlisted", tokens[1].value)
	}
}

func admitCreateObject(tokens []sqlToken, kind StatementKind, targetIndex int) (AdmittedStatement, error) {
	if hasKeywords(tokens, targetIndex, "IF", "NOT", "EXISTS") {
		targetIndex += 3
	}
	return admitTargetAt(tokens, kind, targetIndex)
}

func admitDrop(tokens []sqlToken) (AdmittedStatement, error) {
	if len(tokens) < 3 {
		return AdmittedStatement{}, fmt.Errorf("admit DROP: incomplete statement")
	}
	var kind StatementKind
	switch folded(tokens[1]) {
	case "TABLE":
		kind = StatementDropTable
	case "INDEX":
		kind = StatementDropIndex
	case "VIEW":
		kind = StatementDropView
	default:
		return AdmittedStatement{}, fmt.Errorf("admit DROP: object type %q is not allowlisted", tokens[1].value)
	}
	targetIndex := 2
	if hasKeywords(tokens, targetIndex, "IF", "EXISTS") {
		targetIndex += 2
	}
	return admitTargetAt(tokens, kind, targetIndex)
}

func admitTargetAfter(tokens []sqlToken, kind StatementKind, prefix []string, allowIf bool) (AdmittedStatement, error) {
	if !hasKeywords(tokens, 0, prefix...) {
		return AdmittedStatement{}, fmt.Errorf("admit %s: malformed prefix", kind)
	}
	index := len(prefix)
	if allowIf && hasKeywords(tokens, index, "IF", "EXISTS") {
		index += 2
	}
	return admitTargetAt(tokens, kind, index)
}

func admitTargetAt(tokens []sqlToken, kind StatementKind, index int) (AdmittedStatement, error) {
	if index >= len(tokens) || !isIdentifier(tokens[index]) {
		return AdmittedStatement{}, fmt.Errorf("admit %s: missing target identifier", kind)
	}
	target := tokens[index]
	if index+1 < len(tokens) && tokens[index+1].value == "." {
		if target.quoted || !strings.EqualFold(target.value, "main") {
			return AdmittedStatement{}, fmt.Errorf("admit %s: only unquoted main schema qualifier is allowed", kind)
		}
		if index+2 >= len(tokens) || !isIdentifier(tokens[index+2]) {
			return AdmittedStatement{}, fmt.Errorf("admit %s: missing qualified target", kind)
		}
		if index+3 < len(tokens) && tokens[index+3].value == "." {
			return AdmittedStatement{}, fmt.Errorf("admit %s: target has multiple qualifiers", kind)
		}
		target = tokens[index+2]
	}
	return AdmittedStatement{Kind: kind, Target: target.value}, nil
}

func hasKeywords(tokens []sqlToken, start int, keywords ...string) bool {
	if start < 0 || start+len(keywords) > len(tokens) {
		return false
	}
	for index, keyword := range keywords {
		if folded(tokens[start+index]) != keyword {
			return false
		}
	}
	return true
}

func folded(token sqlToken) string {
	if token.quoted {
		return ""
	}
	return strings.ToUpper(token.value)
}

func isIdentifier(token sqlToken) bool {
	if token.quoted {
		return token.value != ""
	}
	if token.value == "" || !isIdentifierStart(token.value[0]) {
		return false
	}
	for index := 1; index < len(token.value); index++ {
		if !isIdentifierContinue(token.value[index]) {
			return false
		}
	}
	return true
}

func lexSingleStatement(statement string) ([]sqlToken, error) {
	var tokens []sqlToken
	seenSeparator := false
	for index := 0; index < len(statement); {
		current := statement[index]
		if isASCIIWhitespace(current) {
			index++
			continue
		}
		if current == '-' && index+1 < len(statement) && statement[index+1] == '-' {
			index += 2
			for index < len(statement) && statement[index] != '\n' {
				index++
			}
			continue
		}
		if current == '/' && index+1 < len(statement) && statement[index+1] == '*' {
			end := strings.Index(statement[index+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("admit SQL: unclosed block comment")
			}
			index += end + 4
			continue
		}
		if seenSeparator {
			return nil, fmt.Errorf("admit SQL: tokens after statement separator")
		}
		if current == ';' {
			if len(tokens) == 0 {
				return nil, fmt.Errorf("admit SQL: empty statement")
			}
			seenSeparator = true
			index++
			continue
		}
		if current == '\'' {
			end, err := scanDelimited(statement, index, '\'', '\'')
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, sqlToken{value: statement[index:end]})
			index = end
			continue
		}
		if current == '"' || current == '`' {
			end, err := scanDelimited(statement, index, current, current)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, sqlToken{value: unescapeQuoted(statement[index+1:end-1], current), quoted: true})
			index = end
			continue
		}
		if current == '[' {
			end := strings.IndexByte(statement[index+1:], ']')
			if end < 0 {
				return nil, fmt.Errorf("admit SQL: unclosed bracket identifier")
			}
			end += index + 1
			tokens = append(tokens, sqlToken{value: statement[index+1 : end], quoted: true})
			index = end + 1
			continue
		}
		if isIdentifierStart(current) {
			end := index + 1
			for end < len(statement) && isIdentifierContinue(statement[end]) {
				end++
			}
			tokens = append(tokens, sqlToken{value: statement[index:end]})
			index = end
			continue
		}
		tokens = append(tokens, sqlToken{value: string(current)})
		index++
	}
	return tokens, nil
}

func scanDelimited(statement string, start int, delimiter byte, escape byte) (int, error) {
	for index := start + 1; index < len(statement); index++ {
		if statement[index] != delimiter {
			continue
		}
		if index+1 < len(statement) && statement[index+1] == escape {
			index++
			continue
		}
		return index + 1, nil
	}
	return 0, fmt.Errorf("admit SQL: unclosed quoted token")
}

func unescapeQuoted(value string, delimiter byte) string {
	escaped := string([]byte{delimiter, delimiter})
	return strings.ReplaceAll(value, escaped, string(delimiter))
}

func isASCIIWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}

func isIdentifierStart(value byte) bool {
	return value == '_' || value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isIdentifierContinue(value byte) bool {
	return isIdentifierStart(value) || value >= '0' && value <= '9' || value == '$'
}
