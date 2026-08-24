package handler

import "testing"

func TestValidateSchemaTypes_EmptyIsValid(t *testing.T) {
	if err := validateSchemaTypes("CAT-1", nil); err != nil {
		t.Fatalf("expected nil for empty schemaTypes, got %v", err)
	}
}

func TestValidateSchemaTypes_EmptyStringItemIsRejected(t *testing.T) {
	if err := validateSchemaTypes("CAT-1", []string{""}); err == nil {
		t.Fatal("expected an error for an empty-string schemaTypes entry")
	}
}
