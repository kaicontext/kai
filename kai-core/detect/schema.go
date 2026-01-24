// Package detect provides schema and migration change detection.
package detect

import (
	"regexp"
	"strings"
)

// SchemaType represents the type of schema file.
type SchemaType string

const (
	SchemaPrisma    SchemaType = "prisma"
	SchemaSQL       SchemaType = "sql"
	SchemaGraphQL   SchemaType = "graphql"
	SchemaProtobuf  SchemaType = "protobuf"
	SchemaJSONSchema SchemaType = "json-schema"
	SchemaOpenAPI   SchemaType = "openapi"
	SchemaUnknown   SchemaType = "unknown"
)

// SchemaChange represents a detected schema change.
type SchemaChange struct {
	Type       string // "field_added", "field_removed", "field_changed", "table_added", etc.
	EntityName string // Table/model/type name
	FieldName  string // Field/column name (if applicable)
	OldDef     string // Old definition
	NewDef     string // New definition
}

// InferSchemaType determines the schema type from a file path.
func InferSchemaType(path string) SchemaType {
	lowerPath := strings.ToLower(path)

	// Prisma
	if strings.HasSuffix(lowerPath, ".prisma") || strings.Contains(lowerPath, "schema.prisma") {
		return SchemaPrisma
	}

	// SQL migrations
	if strings.HasSuffix(lowerPath, ".sql") {
		if strings.Contains(lowerPath, "migration") ||
			strings.Contains(lowerPath, "migrate") ||
			hasTimestampPrefix(path) {
			return SchemaSQL
		}
	}

	// GraphQL
	if strings.HasSuffix(lowerPath, ".graphql") || strings.HasSuffix(lowerPath, ".gql") {
		return SchemaGraphQL
	}

	// Protobuf
	if strings.HasSuffix(lowerPath, ".proto") {
		return SchemaProtobuf
	}

	// JSON Schema
	if strings.Contains(lowerPath, "schema.json") || strings.Contains(lowerPath, ".schema.json") {
		return SchemaJSONSchema
	}

	// OpenAPI/Swagger
	if strings.Contains(lowerPath, "openapi") || strings.Contains(lowerPath, "swagger") {
		if strings.HasSuffix(lowerPath, ".yaml") || strings.HasSuffix(lowerPath, ".yml") ||
			strings.HasSuffix(lowerPath, ".json") {
			return SchemaOpenAPI
		}
	}

	return SchemaUnknown
}

// hasTimestampPrefix checks if a filename starts with a timestamp (common in migrations).
func hasTimestampPrefix(path string) bool {
	// Get just the filename
	parts := strings.Split(path, "/")
	filename := parts[len(parts)-1]

	// Check for common migration patterns:
	// 20210101120000_create_users.sql
	// 001_create_users.sql
	// V1__create_users.sql
	patterns := []string{
		`^\d{14}_`,  // Timestamp: 20210101120000_
		`^\d{3}_`,   // Sequential: 001_
		`^V\d+__`,   // Flyway: V1__
		`^\d+_`,     // Simple numeric: 1_
	}

	for _, p := range patterns {
		if matched, _ := regexp.MatchString(p, filename); matched {
			return true
		}
	}
	return false
}

// IsMigrationFile checks if a file path represents a database migration.
func IsMigrationFile(path string) bool {
	lowerPath := strings.ToLower(path)

	// Check directory patterns
	migrationDirs := []string{
		"/migrations/",
		"/migrate/",
		"/db/migrate/",
		"/database/migrations/",
		"/prisma/migrations/",
		"/drizzle/",
		"/knex/migrations/",
		"/sequelize/migrations/",
	}

	for _, dir := range migrationDirs {
		if strings.Contains(lowerPath, dir) {
			return true
		}
	}

	// Check file patterns
	if strings.HasSuffix(lowerPath, ".sql") && hasTimestampPrefix(path) {
		return true
	}

	return false
}

// IsSchemaFile checks if a file path represents a schema definition.
func IsSchemaFile(path string) bool {
	schemaType := InferSchemaType(path)
	return schemaType != SchemaUnknown
}

// DetectSchemaChanges detects changes in schema files.
// Returns specialized change signals for schema modifications.
func DetectSchemaChanges(path string, before, after []byte) ([]*ChangeSignal, error) {
	schemaType := InferSchemaType(path)
	isMigration := IsMigrationFile(path)

	var signals []*ChangeSignal

	switch schemaType {
	case SchemaPrisma:
		signals = detectPrismaChanges(path, before, after)
	case SchemaSQL:
		signals = detectSQLChanges(path, before, after, isMigration)
	case SchemaGraphQL:
		signals = detectGraphQLChanges(path, before, after)
	default:
		// For unknown schema types, fall back to line-based diff
		signals = detectGenericSchemaChanges(path, before, after)
	}

	// Enrich all signals with metadata
	for _, sig := range signals {
		EnrichSignalWithMetadata(sig)
	}

	return signals, nil
}

// detectPrismaChanges detects changes in Prisma schema files.
func detectPrismaChanges(path string, before, after []byte) []*ChangeSignal {
	var signals []*ChangeSignal

	beforeModels := extractPrismaModels(string(before))
	afterModels := extractPrismaModels(string(after))

	// Check for removed models
	for name := range beforeModels {
		if _, exists := afterModels[name]; !exists {
			signals = append(signals, &ChangeSignal{
				Category: SchemaFieldRemoved,
				Evidence: ExtendedEvidence{
					FileRanges: []FileRange{{Path: path}},
					Symbols:    []string{"model:" + name},
					OldName:    name,
				},
				Weight:     SignalWeight[SchemaFieldRemoved],
				Confidence: 1.0,
				Tags:       []string{"schema", "breaking"},
			})
		}
	}

	// Check for added/changed models
	for name, afterDef := range afterModels {
		if beforeDef, exists := beforeModels[name]; !exists {
			signals = append(signals, &ChangeSignal{
				Category: SchemaFieldAdded,
				Evidence: ExtendedEvidence{
					FileRanges: []FileRange{{Path: path}},
					Symbols:    []string{"model:" + name},
					NewName:    name,
				},
				Weight:     SignalWeight[SchemaFieldAdded],
				Confidence: 1.0,
				Tags:       []string{"schema"},
			})
		} else if beforeDef != afterDef {
			signals = append(signals, &ChangeSignal{
				Category: SchemaFieldChanged,
				Evidence: ExtendedEvidence{
					FileRanges:  []FileRange{{Path: path}},
					Symbols:     []string{"model:" + name},
					BeforeValue: beforeDef,
					AfterValue:  afterDef,
				},
				Weight:     SignalWeight[SchemaFieldChanged],
				Confidence: 1.0,
				Tags:       []string{"schema"},
			})
		}
	}

	return signals
}

// extractPrismaModels extracts model definitions from Prisma schema.
func extractPrismaModels(content string) map[string]string {
	models := make(map[string]string)

	// Simple regex to extract model blocks
	// model User { ... }
	modelRegex := regexp.MustCompile(`(?s)model\s+(\w+)\s*\{([^}]*)\}`)
	matches := modelRegex.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			name := match[1]
			body := strings.TrimSpace(match[2])
			models[name] = body
		}
	}

	return models
}

// detectSQLChanges detects changes in SQL files.
func detectSQLChanges(path string, before, after []byte, isMigration bool) []*ChangeSignal {
	var signals []*ChangeSignal

	// If it's a new migration file
	if len(before) == 0 && len(after) > 0 {
		signals = append(signals, &ChangeSignal{
			Category: MigrationAdded,
			Evidence: ExtendedEvidence{
				FileRanges: []FileRange{{Path: path}},
				Symbols:    []string{extractMigrationName(path)},
			},
			Weight:     SignalWeight[MigrationAdded],
			Confidence: 1.0,
			Tags:       []string{"schema", "migration"},
		})

		// Analyze the SQL content for specific operations
		sqlOps := analyzeSQLContent(string(after))
		for _, op := range sqlOps {
			signals = append(signals, &ChangeSignal{
				Category: op.Category,
				Evidence: ExtendedEvidence{
					FileRanges: []FileRange{{Path: path}},
					Symbols:    []string{op.Target},
				},
				Weight:     SignalWeight[op.Category],
				Confidence: 0.9,
				Tags:       []string{"schema"},
			})
		}
	} else if len(before) > 0 && len(after) > 0 {
		// Modified migration (unusual but possible)
		signals = append(signals, &ChangeSignal{
			Category: SchemaFieldChanged,
			Evidence: ExtendedEvidence{
				FileRanges: []FileRange{{Path: path}},
				Symbols:    []string{extractMigrationName(path)},
			},
			Weight:     SignalWeight[SchemaFieldChanged],
			Confidence: 1.0,
			Tags:       []string{"schema", "migration"},
		})
	}

	return signals
}

// SQLOperation represents a detected SQL operation.
type SQLOperation struct {
	Category ChangeCategory
	Target   string
}

// analyzeSQLContent analyzes SQL content for schema operations.
func analyzeSQLContent(content string) []SQLOperation {
	var ops []SQLOperation
	upperContent := strings.ToUpper(content)

	// Detect CREATE TABLE
	createTableRegex := regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["\x60]?(\w+)["\x60]?`)
	if matches := createTableRegex.FindAllStringSubmatch(content, -1); matches != nil {
		for _, match := range matches {
			ops = append(ops, SQLOperation{
				Category: SchemaFieldAdded,
				Target:   "table:" + match[1],
			})
		}
	}

	// Detect DROP TABLE
	if strings.Contains(upperContent, "DROP TABLE") {
		dropTableRegex := regexp.MustCompile(`(?i)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?["\x60]?(\w+)["\x60]?`)
		if matches := dropTableRegex.FindAllStringSubmatch(content, -1); matches != nil {
			for _, match := range matches {
				ops = append(ops, SQLOperation{
					Category: SchemaFieldRemoved,
					Target:   "table:" + match[1],
				})
			}
		}
	}

	// Detect ALTER TABLE ADD COLUMN
	if strings.Contains(upperContent, "ALTER TABLE") {
		addColumnRegex := regexp.MustCompile(`(?i)ALTER\s+TABLE\s+["\x60]?(\w+)["\x60]?\s+ADD\s+(?:COLUMN\s+)?["\x60]?(\w+)["\x60]?`)
		if matches := addColumnRegex.FindAllStringSubmatch(content, -1); matches != nil {
			for _, match := range matches {
				ops = append(ops, SQLOperation{
					Category: SchemaFieldAdded,
					Target:   match[1] + "." + match[2],
				})
			}
		}

		// Detect ALTER TABLE DROP COLUMN
		dropColumnRegex := regexp.MustCompile(`(?i)ALTER\s+TABLE\s+["\x60]?(\w+)["\x60]?\s+DROP\s+(?:COLUMN\s+)?["\x60]?(\w+)["\x60]?`)
		if matches := dropColumnRegex.FindAllStringSubmatch(content, -1); matches != nil {
			for _, match := range matches {
				ops = append(ops, SQLOperation{
					Category: SchemaFieldRemoved,
					Target:   match[1] + "." + match[2],
				})
			}
		}
	}

	// Detect CREATE INDEX
	if strings.Contains(upperContent, "CREATE INDEX") || strings.Contains(upperContent, "CREATE UNIQUE INDEX") {
		indexRegex := regexp.MustCompile(`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\s+["\x60]?(\w+)["\x60]?`)
		if matches := indexRegex.FindAllStringSubmatch(content, -1); matches != nil {
			for _, match := range matches {
				ops = append(ops, SQLOperation{
					Category: SchemaFieldAdded,
					Target:   "index:" + match[1],
				})
			}
		}
	}

	return ops
}

// extractMigrationName extracts a meaningful name from a migration file path.
func extractMigrationName(path string) string {
	parts := strings.Split(path, "/")
	filename := parts[len(parts)-1]

	// Remove extension
	if idx := strings.LastIndex(filename, "."); idx > 0 {
		filename = filename[:idx]
	}

	// Remove timestamp prefix
	patterns := []string{
		`^\d{14}_`,  // 20210101120000_
		`^\d{3}_`,   // 001_
		`^V\d+__`,   // V1__
		`^\d+_`,     // 1_
	}

	for _, p := range patterns {
		re := regexp.MustCompile(p)
		if re.MatchString(filename) {
			return re.ReplaceAllString(filename, "")
		}
	}

	return filename
}

// detectGraphQLChanges detects changes in GraphQL schema files.
func detectGraphQLChanges(path string, before, after []byte) []*ChangeSignal {
	var signals []*ChangeSignal

	beforeTypes := extractGraphQLTypes(string(before))
	afterTypes := extractGraphQLTypes(string(after))

	// Check for removed types
	for name := range beforeTypes {
		if _, exists := afterTypes[name]; !exists {
			signals = append(signals, &ChangeSignal{
				Category: SchemaFieldRemoved,
				Evidence: ExtendedEvidence{
					FileRanges: []FileRange{{Path: path}},
					Symbols:    []string{"type:" + name},
					OldName:    name,
				},
				Weight:     SignalWeight[SchemaFieldRemoved],
				Confidence: 1.0,
				Tags:       []string{"schema", "breaking", "api"},
			})
		}
	}

	// Check for added/changed types
	for name, afterDef := range afterTypes {
		if beforeDef, exists := beforeTypes[name]; !exists {
			signals = append(signals, &ChangeSignal{
				Category: SchemaFieldAdded,
				Evidence: ExtendedEvidence{
					FileRanges: []FileRange{{Path: path}},
					Symbols:    []string{"type:" + name},
					NewName:    name,
				},
				Weight:     SignalWeight[SchemaFieldAdded],
				Confidence: 1.0,
				Tags:       []string{"schema", "api"},
			})
		} else if beforeDef != afterDef {
			signals = append(signals, &ChangeSignal{
				Category: SchemaFieldChanged,
				Evidence: ExtendedEvidence{
					FileRanges:  []FileRange{{Path: path}},
					Symbols:     []string{"type:" + name},
					BeforeValue: beforeDef,
					AfterValue:  afterDef,
				},
				Weight:     SignalWeight[SchemaFieldChanged],
				Confidence: 1.0,
				Tags:       []string{"schema", "api"},
			})
		}
	}

	return signals
}

// extractGraphQLTypes extracts type definitions from GraphQL schema.
func extractGraphQLTypes(content string) map[string]string {
	types := make(map[string]string)

	// Extract type, input, interface, enum definitions
	typeRegex := regexp.MustCompile(`(?s)(type|input|interface|enum)\s+(\w+)(?:\s+implements\s+\w+)?\s*\{([^}]*)\}`)
	matches := typeRegex.FindAllStringSubmatch(content, -1)

	for _, match := range matches {
		if len(match) >= 4 {
			kind := match[1]
			name := match[2]
			body := strings.TrimSpace(match[3])
			types[kind+":"+name] = body
		}
	}

	return types
}

// detectGenericSchemaChanges uses line-based diff for unknown schema types.
func detectGenericSchemaChanges(path string, before, after []byte) []*ChangeSignal {
	var signals []*ChangeSignal

	// Simple heuristic: if file is new, it's an addition
	if len(before) == 0 && len(after) > 0 {
		signals = append(signals, &ChangeSignal{
			Category: SchemaFieldAdded,
			Evidence: ExtendedEvidence{
				FileRanges: []FileRange{{Path: path}},
			},
			Weight:     SignalWeight[SchemaFieldAdded],
			Confidence: 0.7,
			Tags:       []string{"schema"},
		})
	} else if len(before) > 0 && len(after) == 0 {
		signals = append(signals, &ChangeSignal{
			Category: SchemaFieldRemoved,
			Evidence: ExtendedEvidence{
				FileRanges: []FileRange{{Path: path}},
			},
			Weight:     SignalWeight[SchemaFieldRemoved],
			Confidence: 0.7,
			Tags:       []string{"schema", "breaking"},
		})
	} else if string(before) != string(after) {
		signals = append(signals, &ChangeSignal{
			Category: SchemaFieldChanged,
			Evidence: ExtendedEvidence{
				FileRanges: []FileRange{{Path: path}},
			},
			Weight:     SignalWeight[SchemaFieldChanged],
			Confidence: 0.6,
			Tags:       []string{"schema"},
		})
	}

	return signals
}
