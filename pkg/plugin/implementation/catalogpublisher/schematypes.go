package catalogpublisher

import (
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaTypesSchemaID is this validator's own internal $id -- never
// fetched externally, just a stable name to compile/reference the
// embedded schema under.
const schemaTypesSchemaID = "https://beckn-onix.internal/schemas/nfh014-schema-types.json"

// schemaTypesSchemaJSON is a custom JSON Schema for
// publishDirectives[].schemaTypes, sourced from RFC NFH-014
// (Decentralized Catalog Publishing and Discovery). beckn.yaml's own
// CatalogPublishAction has no schemaTypes field at all -- it's an
// NFH-014 addition to the catalog index entry, not part of the canonical
// action envelope -- so there is no upstream machine-readable schema to
// point schemaValidator's own auxiliary-spec mechanism at; that
// mechanism also can't help here regardless, since auxiliary specs may
// only add actions beckn.yaml doesn't already define, never extend one
// it does (schemav2validator.loadAllSpecs hard-rejects a collision).
//
// Per NFH-014 ("schemaTypes ... Which domain schema(s) the catalog's
// content conforms to -- the same filtering purpose as networkIds, for a
// DS that only cares about specific domains"): an array of unique,
// non-empty domain-schema context URIs -- see the file spec's own
// examples, e.g. "https://schema.beckn.org/retail/schema/1.1.0/context.jsonld".
const schemaTypesSchemaJSON = `{
	"$id": "` + schemaTypesSchemaID + `",
	"$schema": "http://json-schema.org/draft-07/schema#",
	"title": "NFH-014 schemaTypes",
	"type": "array",
	"items": { "type": "string", "format": "uri", "minLength": 1 },
	"uniqueItems": true
}`

// schemaTypesSchemaOnce compiles schemaTypesSchemaJSON lazily, on first
// use, rather than as a package-level var initializer -- a compile
// failure (e.g. a future edit to schemaTypesSchemaJSON introducing a
// syntax error) then surfaces as an ordinary error from
// validateSchemaTypes instead of a panic at package load time that
// would crash every binary importing this package, not just this one
// validation path.
var (
	schemaTypesSchemaOnce sync.Once
	schemaTypesSchema     *jsonschema.Schema
	schemaTypesSchemaErr  error
)

func compileSchemaTypesSchema() (*jsonschema.Schema, error) {
	schemaTypesSchemaOnce.Do(func() {
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaTypesSchemaJSON))
		if err != nil {
			schemaTypesSchemaErr = fmt.Errorf("catalogpublisher: parsing embedded schemaTypes schema: %w", err)
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		if err := compiler.AddResource(schemaTypesSchemaID, doc); err != nil {
			schemaTypesSchemaErr = fmt.Errorf("catalogpublisher: registering embedded schemaTypes schema: %w", err)
			return
		}
		schema, err := compiler.Compile(schemaTypesSchemaID)
		if err != nil {
			schemaTypesSchemaErr = fmt.Errorf("catalogpublisher: compiling embedded schemaTypes schema: %w", err)
			return
		}
		schemaTypesSchema = schema
	})
	return schemaTypesSchema, schemaTypesSchemaErr
}

// validateSchemaTypes checks schemaTypes against the compiled
// schemaTypesSchema, returning a descriptive error naming catalogID if
// invalid. schemaTypes is optional -- NFH-014 has no "at least one"
// requirement -- so an empty/nil slice is always valid.
func validateSchemaTypes(catalogID string, schemaTypes []string) error {
	if len(schemaTypes) == 0 {
		return nil
	}
	schema, err := compileSchemaTypesSchema()
	if err != nil {
		return fmt.Errorf("publishDirectives entry for catalogId %q: schemaTypes validation unavailable: %w", catalogID, err)
	}
	asAny := make([]any, len(schemaTypes))
	for i, t := range schemaTypes {
		asAny[i] = t
	}
	if err := schema.Validate(asAny); err != nil {
		return fmt.Errorf("publishDirectives entry for catalogId %q: invalid schemaTypes: %w", catalogID, err)
	}
	return nil
}
