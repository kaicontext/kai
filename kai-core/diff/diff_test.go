package diff

import (
	"strings"
	"testing"
)

func TestDiffFile_CodeAddedFunction(t *testing.T) {
	before := []byte(`function foo() {
  return 1;
}`)
	after := []byte(`function foo() {
  return 1;
}

function bar() {
  return 2;
}`)

	d := NewDiffer()
	fd, err := d.DiffFile("test.js", before, after)
	if err != nil {
		t.Fatalf("DiffFile failed: %v", err)
	}

	if fd.Action != ActionModified {
		t.Errorf("expected ActionModified, got %s", fd.Action)
	}

	foundBar := false
	for _, u := range fd.Units {
		if u.Name == "bar" && u.Action == ActionAdded {
			foundBar = true
		}
	}
	if !foundBar {
		t.Error("expected to find added function 'bar'")
	}
}

func TestDiffFile_CodeRemovedFunction(t *testing.T) {
	before := []byte(`function foo() {
  return 1;
}

function bar() {
  return 2;
}`)
	after := []byte(`function foo() {
  return 1;
}`)

	d := NewDiffer()
	fd, err := d.DiffFile("test.js", before, after)
	if err != nil {
		t.Fatalf("DiffFile failed: %v", err)
	}

	foundBar := false
	for _, u := range fd.Units {
		if u.Name == "bar" && u.Action == ActionRemoved {
			foundBar = true
		}
	}
	if !foundBar {
		t.Error("expected to find removed function 'bar'")
	}
}

func TestDiffFile_CodeModifiedFunction(t *testing.T) {
	before := []byte(`function foo(a) {
  return a;
}`)
	after := []byte(`function foo(a, b) {
  return a + b;
}`)

	d := NewDiffer()
	fd, err := d.DiffFile("test.js", before, after)
	if err != nil {
		t.Fatalf("DiffFile failed: %v", err)
	}

	foundFoo := false
	for _, u := range fd.Units {
		if u.Name == "foo" && u.Action == ActionModified {
			foundFoo = true
			if u.ChangeType != "API_SURFACE_CHANGED" {
				t.Errorf("expected API_SURFACE_CHANGED, got %s", u.ChangeType)
			}
		}
	}
	if !foundFoo {
		t.Error("expected to find modified function 'foo'")
	}
}

func TestDiffFile_JSON(t *testing.T) {
	before := []byte(`{"timeout": 3600, "debug": false}`)
	after := []byte(`{"timeout": 1800, "debug": false, "retries": 3}`)

	d := NewDiffer()
	fd, err := d.DiffFile("config.json", before, after)
	if err != nil {
		t.Fatalf("DiffFile failed: %v", err)
	}

	if fd.Lang != "json" {
		t.Errorf("expected lang 'json', got %s", fd.Lang)
	}

	if len(fd.Units) == 0 {
		t.Error("expected units in JSON diff")
	}
}

func TestDiffFile_SQL(t *testing.T) {
	before := []byte(`CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email VARCHAR(100) NOT NULL
);`)
	after := []byte(`CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email VARCHAR(255) NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);`)

	d := NewDiffer()
	fd, err := d.DiffFile("schema.sql", before, after)
	if err != nil {
		t.Fatalf("DiffFile failed: %v", err)
	}

	if fd.Lang != "sql" {
		t.Errorf("expected lang 'sql', got %s", fd.Lang)
	}

	foundEmailChange := false
	foundCreatedAt := false
	for _, u := range fd.Units {
		if u.Path == "users.email" && u.Action == ActionModified {
			foundEmailChange = true
		}
		if u.Path == "users.created_at" && u.Action == ActionAdded {
			foundCreatedAt = true
		}
	}

	if !foundEmailChange {
		t.Error("expected to find modified column 'users.email'")
	}
	if !foundCreatedAt {
		t.Error("expected to find added column 'users.created_at'")
	}
}

func TestDiffFile_SQLTableAdded(t *testing.T) {
	before := []byte(`CREATE TABLE users (
  id INTEGER PRIMARY KEY
);`)
	after := []byte(`CREATE TABLE users (
  id INTEGER PRIMARY KEY
);

CREATE TABLE posts (
  id INTEGER PRIMARY KEY,
  user_id INTEGER,
  title VARCHAR(255)
);`)

	d := NewDiffer()
	fd, err := d.DiffFile("schema.sql", before, after)
	if err != nil {
		t.Fatalf("DiffFile failed: %v", err)
	}

	foundPosts := false
	for _, u := range fd.Units {
		if u.Name == "posts" && u.Kind == KindSQLTable && u.Action == ActionAdded {
			foundPosts = true
		}
	}
	if !foundPosts {
		t.Error("expected to find added table 'posts'")
	}
}

func TestSemanticDiff_FormatText(t *testing.T) {
	sd := &SemanticDiff{
		Files: []FileDiff{
			{
				Path:   "auth/login.ts",
				Action: ActionModified,
				Lang:   "ts",
				Units: []UnitDiff{
					{Kind: KindFunction, Name: "login", Action: ActionModified, BeforeSig: "login(user)", AfterSig: "login(user, token)"},
					{Kind: KindFunction, Name: "validateMFA", Action: ActionAdded, AfterSig: "validateMFA(code)"},
				},
			},
			{
				Path:   "config.json",
				Action: ActionModified,
				Lang:   "json",
				Units: []UnitDiff{
					{Kind: KindJSONKey, Path: "timeout", Action: ActionModified, Before: "3600", After: "1800"},
				},
			},
		},
	}
	sd.ComputeSummary()

	output := sd.FormatText()

	if !strings.Contains(output, "auth/login.ts") {
		t.Error("expected output to contain 'auth/login.ts'")
	}
	if !strings.Contains(output, "login(user) -> login(user, token)") {
		t.Error("expected output to contain signature change")
	}
	if !strings.Contains(output, "validateMFA") {
		t.Error("expected output to contain 'validateMFA'")
	}
	if !strings.Contains(output, "timeout: 3600 -> 1800") {
		t.Error("expected output to contain timeout change")
	}
}

func TestSemanticDiff_FormatJSON(t *testing.T) {
	sd := &SemanticDiff{
		Files: []FileDiff{
			{
				Path:   "test.js",
				Action: ActionModified,
				Units: []UnitDiff{
					{Kind: KindFunction, Name: "foo", Action: ActionAdded},
				},
			},
		},
	}
	sd.ComputeSummary()

	jsonBytes, err := sd.FormatJSON()
	if err != nil {
		t.Fatalf("FormatJSON failed: %v", err)
	}

	if !strings.Contains(string(jsonBytes), "test.js") {
		t.Error("expected JSON to contain 'test.js'")
	}
	if !strings.Contains(string(jsonBytes), "function") {
		t.Error("expected JSON to contain 'function'")
	}
}

func TestParseSQL(t *testing.T) {
	sql := `
CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email VARCHAR(255) NOT NULL,
  name TEXT,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE posts (
  id INTEGER PRIMARY KEY,
  user_id INTEGER,
  title VARCHAR(255)
);
`

	tables := parseSQL(sql)

	if len(tables) != 2 {
		t.Errorf("expected 2 tables, got %d", len(tables))
	}

	users, ok := tables["users"]
	if !ok {
		t.Fatal("expected 'users' table")
	}

	if len(users.Columns) < 3 {
		t.Errorf("expected at least 3 columns in users, got %d", len(users.Columns))
	}

	emailCol, ok := users.Columns["email"]
	if !ok {
		t.Error("expected 'email' column")
	} else if emailCol.Nullable {
		t.Error("expected email to be NOT NULL")
	}
}
