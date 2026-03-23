package schema

import "testing"

func TestParseTracksUniqueConstraints(t *testing.T) {
	t.Parallel()

	schema, err := Parse(`
CREATE TABLE demo (
    email text UNIQUE,
    org_id text,
    name text,
    UNIQUE (org_id, name)
);
`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(schema.Tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(schema.Tables))
	}

	table := schema.Tables[0]
	if !contains(table.Columns[0].Constraints, "unique") {
		t.Fatalf("expected email column to be marked unique, got %v", table.Columns[0].Constraints)
	}

	if len(table.UniqueConstraints) != 1 {
		t.Fatalf("expected 1 composite unique constraint, got %d", len(table.UniqueConstraints))
	}
}
