package detect

import "testing"

func TestInferSchemaType(t *testing.T) {
	tests := []struct {
		path     string
		expected SchemaType
	}{
		{"prisma/schema.prisma", SchemaPrisma},
		{"db/schema.prisma", SchemaPrisma},
		{"migrations/20210101_create_users.sql", SchemaSQL},
		{"db/migrate/001_init.sql", SchemaSQL},
		{"schema.graphql", SchemaGraphQL},
		{"types.gql", SchemaGraphQL},
		{"api.proto", SchemaProtobuf},
		{"user.schema.json", SchemaJSONSchema},
		{"openapi.yaml", SchemaOpenAPI},
		{"swagger.json", SchemaOpenAPI},
		{"random.txt", SchemaUnknown},
	}

	for _, tc := range tests {
		result := InferSchemaType(tc.path)
		if result != tc.expected {
			t.Errorf("InferSchemaType(%q) = %q, expected %q", tc.path, result, tc.expected)
		}
	}
}

func TestIsMigrationFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"db/migrations/20210101_create_users.sql", true},
		{"prisma/migrations/20210101_init/migration.sql", true},
		{"database/migrations/001_init.sql", true},
		{"drizzle/0000_create_table.sql", true},
		{"src/utils.js", false},
		{"config.json", false},
	}

	for _, tc := range tests {
		result := IsMigrationFile(tc.path)
		if result != tc.expected {
			t.Errorf("IsMigrationFile(%q) = %v, expected %v", tc.path, result, tc.expected)
		}
	}
}

func TestHasTimestampPrefix(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"20210101120000_create_users.sql", true},
		{"001_init.sql", true},
		{"V1__baseline.sql", true},
		{"1_create_table.sql", true},
		{"create_users.sql", false},
		{"utils.js", false},
	}

	for _, tc := range tests {
		result := hasTimestampPrefix(tc.path)
		if result != tc.expected {
			t.Errorf("hasTimestampPrefix(%q) = %v, expected %v", tc.path, result, tc.expected)
		}
	}
}

func TestExtractPrismaModels(t *testing.T) {
	content := `
model User {
  id    Int     @id @default(autoincrement())
  email String  @unique
  name  String?
}

model Post {
  id        Int      @id @default(autoincrement())
  title     String
  content   String?
}
`
	models := extractPrismaModels(content)

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if _, ok := models["User"]; !ok {
		t.Error("expected User model")
	}
	if _, ok := models["Post"]; !ok {
		t.Error("expected Post model")
	}
}

func TestExtractGraphQLTypes(t *testing.T) {
	content := `
type User {
  id: ID!
  name: String!
  email: String!
}

type Query {
  user(id: ID!): User
  users: [User!]!
}

input CreateUserInput {
  name: String!
  email: String!
}
`
	types := extractGraphQLTypes(content)

	if len(types) != 3 {
		t.Fatalf("expected 3 types, got %d", len(types))
	}
	if _, ok := types["type:User"]; !ok {
		t.Error("expected type:User")
	}
	if _, ok := types["type:Query"]; !ok {
		t.Error("expected type:Query")
	}
	if _, ok := types["input:CreateUserInput"]; !ok {
		t.Error("expected input:CreateUserInput")
	}
}

func TestAnalyzeSQLContent(t *testing.T) {
	content := `
CREATE TABLE users (
  id SERIAL PRIMARY KEY,
  email VARCHAR(255) NOT NULL UNIQUE,
  created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);

ALTER TABLE users ADD COLUMN name VARCHAR(255);
`
	ops := analyzeSQLContent(content)

	// Should detect CREATE TABLE, CREATE INDEX, and ALTER TABLE ADD COLUMN
	if len(ops) < 3 {
		t.Fatalf("expected at least 3 operations, got %d", len(ops))
	}

	// Check that we detected the table creation
	foundTable := false
	for _, op := range ops {
		if op.Target == "table:users" && op.Category == SchemaFieldAdded {
			foundTable = true
			break
		}
	}
	if !foundTable {
		t.Error("expected to detect CREATE TABLE users")
	}
}

func TestDetectSchemaChanges_PrismaModelAdded(t *testing.T) {
	before := `
model User {
  id    Int     @id
  email String
}
`
	after := `
model User {
  id    Int     @id
  email String
}

model Post {
  id    Int     @id
  title String
}
`
	signals, err := DetectSchemaChanges("schema.prisma", []byte(before), []byte(after))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(signals) == 0 {
		t.Fatal("expected at least 1 signal")
	}

	// Should detect Post model added
	foundPost := false
	for _, sig := range signals {
		for _, sym := range sig.Evidence.Symbols {
			if sym == "model:Post" && sig.Category == SchemaFieldAdded {
				foundPost = true
				break
			}
		}
	}
	if !foundPost {
		t.Error("expected to detect Post model added")
	}
}

func TestDetectSchemaChanges_PrismaModelRemoved(t *testing.T) {
	before := `
model User {
  id    Int     @id
}

model Post {
  id    Int     @id
}
`
	after := `
model User {
  id    Int     @id
}
`
	signals, err := DetectSchemaChanges("schema.prisma", []byte(before), []byte(after))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should detect Post model removed
	foundRemoved := false
	for _, sig := range signals {
		if sig.Category == SchemaFieldRemoved {
			for _, sym := range sig.Evidence.Symbols {
				if sym == "model:Post" {
					foundRemoved = true
					break
				}
			}
		}
	}
	if !foundRemoved {
		t.Error("expected to detect Post model removed")
	}

	// Should have breaking tag
	for _, sig := range signals {
		if sig.Category == SchemaFieldRemoved {
			if !sig.HasTag("breaking") {
				t.Error("expected breaking tag for removed model")
			}
		}
	}
}

func TestDetectSchemaChanges_NewMigration(t *testing.T) {
	before := []byte{}
	after := []byte(`
CREATE TABLE users (
  id SERIAL PRIMARY KEY,
  email VARCHAR(255)
);
`)
	signals, err := DetectSchemaChanges("migrations/20210101_create_users.sql", before, after)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(signals) == 0 {
		t.Fatal("expected at least 1 signal")
	}

	// Should have MigrationAdded signal
	foundMigration := false
	for _, sig := range signals {
		if sig.Category == MigrationAdded {
			foundMigration = true
			if !sig.HasTag("migration") {
				t.Error("expected migration tag")
			}
		}
	}
	if !foundMigration {
		t.Error("expected MigrationAdded signal")
	}
}

func TestExtractMigrationName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"migrations/20210101120000_create_users.sql", "create_users"},
		{"db/migrate/001_init.sql", "init"},
		{"V1__baseline.sql", "baseline"},
		{"1_create_table.sql", "create_table"},
		{"create_users.sql", "create_users"},
	}

	for _, tc := range tests {
		result := extractMigrationName(tc.path)
		if result != tc.expected {
			t.Errorf("extractMigrationName(%q) = %q, expected %q", tc.path, result, tc.expected)
		}
	}
}
