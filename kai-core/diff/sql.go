package diff

import (
	"regexp"
	"strings"
)

// sqlTable represents a parsed SQL table definition.
type sqlTable struct {
	Name       string
	Definition string
	Columns    map[string]*sqlColumn
}

// sqlColumn represents a parsed SQL column definition.
type sqlColumn struct {
	Name       string
	Type       string
	Definition string
	Nullable   bool
	Default    string
}

var createTableStartRe = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["'\x60]?(\w+)["'\x60]?\s*\(`)

// parseSQL extracts table and column definitions from SQL content.
// Uses manual parsing to handle nested parentheses in column types like VARCHAR(255).
func parseSQL(content string) map[string]*sqlTable {
	tables := make(map[string]*sqlTable)

	// Find all CREATE TABLE starts
	matches := createTableStartRe.FindAllStringSubmatchIndex(content, -1)
	for _, match := range matches {
		// match[0]:match[1] is the full match
		// match[2]:match[3] is the table name
		tableName := strings.ToLower(content[match[2]:match[3]])
		startIdx := match[1] // Position right after the opening (

		// Find the matching closing ) by counting parentheses
		depth := 1
		endIdx := startIdx
		for i := startIdx; i < len(content) && depth > 0; i++ {
			switch content[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					endIdx = i
				}
			}
		}

		if endIdx <= startIdx {
			continue // Malformed SQL
		}

		columnsDef := content[startIdx:endIdx]
		tableDef := content[match[0]:endIdx+1]

		table := &sqlTable{
			Name:       tableName,
			Definition: strings.TrimSpace(tableDef),
			Columns:    make(map[string]*sqlColumn),
		}

		// Parse columns
		parseTableColumns(columnsDef, table)
		tables[tableName] = table
	}

	return tables
}

// parseTableColumns extracts column definitions from CREATE TABLE body.
func parseTableColumns(columnsDef string, table *sqlTable) {
	// Split by comma, but be careful with parentheses (for types like VARCHAR(255))
	parts := splitColumns(columnsDef)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Skip constraints
		upperPart := strings.ToUpper(part)
		if strings.HasPrefix(upperPart, "PRIMARY KEY") ||
			strings.HasPrefix(upperPart, "FOREIGN KEY") ||
			strings.HasPrefix(upperPart, "UNIQUE") ||
			strings.HasPrefix(upperPart, "CHECK") ||
			strings.HasPrefix(upperPart, "CONSTRAINT") ||
			strings.HasPrefix(upperPart, "INDEX") ||
			strings.HasPrefix(upperPart, "KEY ") {
			continue
		}

		// Parse column definition
		col := parseColumnDef(part)
		if col != nil {
			table.Columns[col.Name] = col
		}
	}
}

// splitColumns splits column definitions by comma, respecting parentheses.
func splitColumns(s string) []string {
	var parts []string
	var current strings.Builder
	depth := 0

	for _, ch := range s {
		switch ch {
		case '(':
			depth++
			current.WriteRune(ch)
		case ')':
			depth--
			current.WriteRune(ch)
		case ',':
			if depth == 0 {
				parts = append(parts, current.String())
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// parseColumnDef parses a single column definition.
func parseColumnDef(def string) *sqlColumn {
	def = strings.TrimSpace(def)
	if def == "" {
		return nil
	}

	// Tokenize: split by whitespace but keep parenthesized parts together
	tokens := tokenizeColumnDef(def)
	if len(tokens) < 2 {
		return nil
	}

	// First token is column name (remove quotes)
	name := strings.Trim(tokens[0], "\"`'")
	name = strings.ToLower(name)

	// Skip if it's a SQL keyword
	upperName := strings.ToUpper(name)
	if upperName == "PRIMARY" || upperName == "FOREIGN" || upperName == "UNIQUE" ||
		upperName == "CHECK" || upperName == "CONSTRAINT" || upperName == "INDEX" || upperName == "KEY" {
		return nil
	}

	// Second token is type (possibly with size)
	colType := tokens[1]

	col := &sqlColumn{
		Name:       name,
		Type:       colType,
		Definition: def,
		Nullable:   true,
	}

	// Check for NOT NULL
	upperDef := strings.ToUpper(def)
	if strings.Contains(upperDef, "NOT NULL") {
		col.Nullable = false
	}

	// Check for DEFAULT
	if idx := strings.Index(upperDef, "DEFAULT "); idx != -1 {
		rest := def[idx+8:]
		// Extract default value (until next keyword or end)
		endIdx := len(rest)
		for _, kw := range []string{" NOT ", " NULL", " PRIMARY", " UNIQUE", " REFERENCES", " CHECK", ","} {
			if kwIdx := strings.Index(strings.ToUpper(rest), kw); kwIdx != -1 && kwIdx < endIdx {
				endIdx = kwIdx
			}
		}
		col.Default = strings.TrimSpace(rest[:endIdx])
	}

	return col
}

// tokenizeColumnDef splits a column definition into tokens.
func tokenizeColumnDef(def string) []string {
	var tokens []string
	var current strings.Builder
	depth := 0
	inQuote := false
	quoteChar := rune(0)

	for _, ch := range def {
		switch {
		case ch == '"' || ch == '\'' || ch == '`':
			if !inQuote {
				inQuote = true
				quoteChar = ch
			} else if ch == quoteChar {
				inQuote = false
			}
			current.WriteRune(ch)
		case ch == '(':
			depth++
			current.WriteRune(ch)
		case ch == ')':
			depth--
			current.WriteRune(ch)
		case (ch == ' ' || ch == '\t' || ch == '\n') && depth == 0 && !inQuote:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(ch)
		}
	}

	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}

	return tokens
}

// SQLDiff represents changes between two SQL schemas.
type SQLDiff struct {
	TablesAdded    []string
	TablesRemoved  []string
	ColumnsAdded   map[string][]string // table -> columns
	ColumnsRemoved map[string][]string
	ColumnsChanged map[string][]ColumnChange
}

// ColumnChange represents a change to a column definition.
type ColumnChange struct {
	Column string
	Before string
	After  string
}

// ComputeSQLDiff compares two SQL schemas.
func ComputeSQLDiff(before, after string) *SQLDiff {
	beforeTables := parseSQL(before)
	afterTables := parseSQL(after)

	diff := &SQLDiff{
		ColumnsAdded:   make(map[string][]string),
		ColumnsRemoved: make(map[string][]string),
		ColumnsChanged: make(map[string][]ColumnChange),
	}

	// Find added tables
	for name := range afterTables {
		if _, exists := beforeTables[name]; !exists {
			diff.TablesAdded = append(diff.TablesAdded, name)
		}
	}

	// Find removed tables
	for name := range beforeTables {
		if _, exists := afterTables[name]; !exists {
			diff.TablesRemoved = append(diff.TablesRemoved, name)
		}
	}

	// Find column changes in existing tables
	for name, afterTable := range afterTables {
		beforeTable, exists := beforeTables[name]
		if !exists {
			continue
		}

		// Added columns
		for colName := range afterTable.Columns {
			if _, exists := beforeTable.Columns[colName]; !exists {
				diff.ColumnsAdded[name] = append(diff.ColumnsAdded[name], colName)
			}
		}

		// Removed columns
		for colName := range beforeTable.Columns {
			if _, exists := afterTable.Columns[colName]; !exists {
				diff.ColumnsRemoved[name] = append(diff.ColumnsRemoved[name], colName)
			}
		}

		// Changed columns
		for colName, afterCol := range afterTable.Columns {
			beforeCol, exists := beforeTable.Columns[colName]
			if exists && afterCol.Definition != beforeCol.Definition {
				diff.ColumnsChanged[name] = append(diff.ColumnsChanged[name], ColumnChange{
					Column: colName,
					Before: beforeCol.Definition,
					After:  afterCol.Definition,
				})
			}
		}
	}

	return diff
}
