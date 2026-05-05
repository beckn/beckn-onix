package schemav2validator

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
)

func TestIsCoreSchema(t *testing.T) {
	tests := []struct {
		name       string
		contextURL string
		want       bool
	}{
		{
			name:       "core schema URL",
			contextURL: "https://raw.githubusercontent.com/beckn/protocol-specifications-new/refs/heads/draft/schema/core/v2/context.jsonld",
			want:       true,
		},
		{
			name:       "domain schema URL",
			contextURL: "https://raw.githubusercontent.com/beckn/protocol-specifications-new/refs/heads/draft/schema/EvChargingOffer/v1/context.jsonld",
			want:       false,
		},
		{
			name:       "empty URL",
			contextURL: "",
			want:       false,
		},
		{
			name:       "URL without schema/core",
			contextURL: "https://example.com/some/path/context.jsonld",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCoreSchema(tt.contextURL)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindReferencedObjects(t *testing.T) {
	tests := []struct {
		name string
		data interface{}
		path string
		want int // number of objects found
	}{
		{
			name: "single domain object",
			data: map[string]interface{}{
				"@context": "https://example.com/schema/DomainType/v1/context.jsonld",
				"@type":    "DomainType",
				"field":    "value",
			},
			path: "message",
			want: 1,
		},
		{
			name: "core schema object - should be skipped",
			data: map[string]interface{}{
				"@context": "https://example.com/schema/core/v2/context.jsonld",
				"@type":    "beckn:Order",
				"field":    "value",
			},
			path: "message",
			want: 0,
		},
		{
			name: "nested domain objects",
			data: map[string]interface{}{
				"order": map[string]interface{}{
					"@context": "https://example.com/schema/core/v2/context.jsonld",
					"@type":    "beckn:Order",
					"orderAttributes": map[string]interface{}{
						"@context": "https://example.com/schema/ChargingSession/v1/context.jsonld",
						"@type":    "ChargingSession",
						"field":    "value",
					},
				},
			},
			path: "message",
			want: 1, // Only domain object, core skipped
		},
		{
			name: "array with domain objects",
			data: map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{
						"@context": "https://example.com/schema/DomainType/v1/context.jsonld",
						"@type":    "DomainType",
					},
					map[string]interface{}{
						"@context": "https://example.com/schema/AnotherType/v1/context.jsonld",
						"@type":    "AnotherType",
					},
				},
			},
			path: "message",
			want: 2,
		},
		{
			name: "object without @context",
			data: map[string]interface{}{
				"field": "value",
			},
			path: "message",
			want: 0,
		},
		{
			name: "object with @context but no @type",
			data: map[string]interface{}{
				"@context": "https://example.com/schema/DomainType/v1/context.jsonld",
				"field":    "value",
			},
			path: "message",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findReferencedObjects(tt.data, tt.path)
			assert.Equal(t, tt.want, len(got))
		})
	}
}

func TestTransformContextToSchemaURL(t *testing.T) {
	tests := []struct {
		name       string
		contextURL string
		want       string
	}{
		{
			name:       "standard transformation",
			contextURL: "https://example.com/schema/EvChargingOffer/v1/context.jsonld",
			want:       "https://example.com/schema/EvChargingOffer/v1/attributes.yaml",
		},
		{
			name:       "already attributes.yaml",
			contextURL: "https://example.com/schema/EvChargingOffer/v1/attributes.yaml",
			want:       "https://example.com/schema/EvChargingOffer/v1/attributes.yaml",
		},
		{
			name:       "no context.jsonld in URL",
			contextURL: "https://example.com/schema/EvChargingOffer/v1/",
			want:       "https://example.com/schema/EvChargingOffer/v1/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := transformContextToSchemaURL(tt.contextURL)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHashURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{
			name: "consistent hashing",
			url:  "https://example.com/schema.yaml",
		},
		{
			name: "empty string",
			url:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1 := hashURL(tt.url)
			hash2 := hashURL(tt.url)
			
			// Same URL should produce same hash
			assert.Equal(t, hash1, hash2)
			
			// Hash should be 64 characters (SHA256 hex)
			assert.Equal(t, 64, len(hash1))
		})
	}
}

func TestIsValidSchemaPath(t *testing.T) {
	tests := []struct {
		name       string
		schemaPath string
		want       bool
	}{
		{
			name:       "http URL",
			schemaPath: "http://example.com/schema.yaml",
			want:       true,
		},
		{
			name:       "https URL",
			schemaPath: "https://example.com/schema.yaml",
			want:       true,
		},
		{
			name:       "file URL",
			schemaPath: "file:///path/to/schema.yaml",
			want:       true,
		},
		{
			name:       "local path",
			schemaPath: "/path/to/schema.yaml",
			want:       true,
		},
		{
			name:       "relative path",
			schemaPath: "./schema.yaml",
			want:       true,
		},
		{
			name:       "empty path",
			schemaPath: "",
			want:       true, // url.Parse("") succeeds, returns empty scheme
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidSchemaPath(tt.schemaPath)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewSchemaCache(t *testing.T) {
	tests := []struct {
		name    string
		maxSize int
	}{
		{
			name:    "default size",
			maxSize: 100,
		},
		{
			name:    "custom size",
			maxSize: 50,
		},
		{
			name:    "zero size",
			maxSize: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newSchemaCache(tt.maxSize)

			assert.NotNil(t, cache)
			assert.Equal(t, tt.maxSize, cache.maxSize)
			assert.NotNil(t, cache.schemas)
			assert.Equal(t, 0, len(cache.schemas))
			assert.NotNil(t, cache.rawSchemas)
			assert.Equal(t, 0, len(cache.rawSchemas))
		})
	}
}

func TestSchemaCache_GetSet(t *testing.T) {
	cache := newSchemaCache(10)
	
	// Create a simple schema doc
	doc := &openapi3.T{
		OpenAPI: "3.1.0",
	}
	
	urlHash := hashURL("https://example.com/schema.yaml")
	ttl := 1 * time.Hour
	
	// Test Set
	cache.set(urlHash, doc, ttl)
	
	// Test Get - should find it
	retrieved, found := cache.get(urlHash)
	assert.True(t, found)
	assert.Equal(t, doc, retrieved)
	
	// Test Get - non-existent key
	_, found = cache.get("non-existent-hash")
	assert.False(t, found)
}

func TestSchemaCache_LRUEviction(t *testing.T) {
	cache := newSchemaCache(2) // Small cache for testing
	
	doc1 := &openapi3.T{OpenAPI: "3.1.0"}
	doc2 := &openapi3.T{OpenAPI: "3.1.1"}
	doc3 := &openapi3.T{OpenAPI: "3.1.2"}
	
	ttl := 1 * time.Hour
	
	// Add first two items
	cache.set("hash1", doc1, ttl)
	cache.set("hash2", doc2, ttl)
	
	// Access first item to make it more recent
	cache.get("hash1")
	
	// Add third item - should evict hash2 (least recently used)
	cache.set("hash3", doc3, ttl)
	
	// Verify hash1 and hash3 exist, hash2 was evicted
	_, found1 := cache.get("hash1")
	_, found2 := cache.get("hash2")
	_, found3 := cache.get("hash3")
	
	assert.True(t, found1, "hash1 should exist (recently accessed)")
	assert.False(t, found2, "hash2 should be evicted (LRU)")
	assert.True(t, found3, "hash3 should exist (just added)")
}

func TestSchemaCache_TTLExpiry(t *testing.T) {
	cache := newSchemaCache(10)
	
	doc := &openapi3.T{OpenAPI: "3.1.0"}
	urlHash := "test-hash"
	
	// Set with very short TTL
	cache.set(urlHash, doc, 1*time.Millisecond)
	
	// Should be found immediately
	_, found := cache.get(urlHash)
	assert.True(t, found)
	
	// Wait for expiry
	time.Sleep(10 * time.Millisecond)
	
	// Should not be found after expiry
	_, found = cache.get(urlHash)
	assert.False(t, found)
}

func TestSchemaCache_CleanupExpired(t *testing.T) {
	cache := newSchemaCache(10)
	
	doc := &openapi3.T{OpenAPI: "3.1.0"}
	
	// Add items with short TTL
	cache.set("hash1", doc, 1*time.Millisecond)
	cache.set("hash2", doc, 1*time.Millisecond)
	cache.set("hash3", doc, 1*time.Hour) // This one won't expire
	
	// Wait for expiry
	time.Sleep(10 * time.Millisecond)
	
	// Cleanup expired
	count := cache.cleanupExpired()
	
	// Should have cleaned up 2 expired items
	assert.Equal(t, 2, count)
	
	// Verify only hash3 remains
	cache.mu.RLock()
	assert.Equal(t, 1, len(cache.schemas))
	_, exists := cache.schemas["hash3"]
	assert.True(t, exists)
	cache.mu.RUnlock()
}

func TestIsAllowedDomain(t *testing.T) {
	tests := []struct {
		name           string
		schemaURL      string
		allowedDomains []string
		want           bool
	}{
		{
			name:           "empty whitelist - all allowed",
			schemaURL:      "https://example.com/schema.yaml",
			allowedDomains: []string{},
			want:           true,
		},
		{
			name:           "nil whitelist - all allowed",
			schemaURL:      "https://example.com/schema.yaml",
			allowedDomains: nil,
			want:           true,
		},
		{
			name:           "domain in whitelist",
			schemaURL:      "https://raw.githubusercontent.com/beckn/schema.yaml",
			allowedDomains: []string{"raw.githubusercontent.com", "schemas.beckn.org"},
			want:           true,
		},
		{
			name:           "domain not in whitelist",
			schemaURL:      "https://malicious.com/schema.yaml",
			allowedDomains: []string{"raw.githubusercontent.com", "schemas.beckn.org"},
			want:           false,
		},
		{
			name:           "partial domain match",
			schemaURL:      "https://raw.githubusercontent.com/beckn/schema.yaml",
			allowedDomains: []string{"githubusercontent.com"},
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedDomain(tt.schemaURL, tt.allowedDomains)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFindReferencedObjects_PathBuilding(t *testing.T) {
	data := map[string]interface{}{
		"order": map[string]interface{}{
			"beckn:orderItems": []interface{}{
				map[string]interface{}{
					"beckn:acceptedOffer": map[string]interface{}{
						"beckn:offerAttributes": map[string]interface{}{
							"@context": "https://example.com/schema/ChargingOffer/v1/context.jsonld",
							"@type":    "ChargingOffer",
						},
					},
				},
			},
		},
	}

	objects := findReferencedObjects(data, "message")
	
	assert.Equal(t, 1, len(objects))
	assert.Equal(t, "message.order.beckn:orderItems[0].beckn:acceptedOffer.beckn:offerAttributes", objects[0].Path)
	assert.Equal(t, "ChargingOffer", objects[0].Type)
}

// Integration tests for the 4 remaining functions

func TestLoadSchemaFromPath_LocalFile(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	
	schemaContent := `openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object
      properties:
        field1:
          type: string`
	
	_, err = tmpFile.Write([]byte(schemaContent))
	assert.NoError(t, err)
	tmpFile.Close()
	
	doc, err := cache.loadSchemaFromPath(ctx, tmpFile.Name(), 1*time.Hour, 30*time.Second, false)
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, "3.1.0", doc.OpenAPI)
}

func TestLoadSchemaFromPath_CacheHit(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	
	schemaContent := `openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0`
	
	tmpFile.Write([]byte(schemaContent))
	tmpFile.Close()
	
	doc1, err := cache.loadSchemaFromPath(ctx, tmpFile.Name(), 1*time.Hour, 30*time.Second, false)
	assert.NoError(t, err)

	doc2, err := cache.loadSchemaFromPath(ctx, tmpFile.Name(), 1*time.Hour, 30*time.Second, false)
	assert.NoError(t, err)
	
	assert.Equal(t, doc1, doc2)
}

func TestLoadSchemaFromPath_InvalidPath(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	_, err := cache.loadSchemaFromPath(ctx, "/nonexistent/schema.yaml", 1*time.Hour, 30*time.Second, false)
	assert.Error(t, err)
}

func TestFindSchemaByType_DirectMatch(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	
	schemaContent := `openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object
      properties:
        field1:
          type: string`
	
	tmpFile.Write([]byte(schemaContent))
	tmpFile.Close()
	
	doc, err := cache.loadSchemaFromPath(ctx, tmpFile.Name(), 1*time.Hour, 30*time.Second, false)
	assert.NoError(t, err)

	schema, err := findSchemaByType(ctx, doc, "TestType")
	assert.NoError(t, err)
	assert.NotNil(t, schema)
}

func TestFindSchemaByType_NotFound(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	
	schemaContent := `openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object`
	
	tmpFile.Write([]byte(schemaContent))
	tmpFile.Close()
	
	doc, err := cache.loadSchemaFromPath(ctx, tmpFile.Name(), 1*time.Hour, 30*time.Second, false)
	assert.NoError(t, err)

	_, err = findSchemaByType(ctx, doc, "NonExistentType")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no schema found")
}

func TestValidateReferencedObject_Valid(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	
	schemaContent := `openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object
      additionalProperties: false
      x-jsonld:
        "@context": ./context.jsonld
        "@type": TestType
      properties:
        field1:
          type: string
      required:
        - field1`
	
	tmpFile.Write([]byte(schemaContent))
	tmpFile.Close()
	
	obj := referencedObject{
		Path:    "message.test",
		Context: tmpFile.Name(),
		Type:    "TestType",
		Data: map[string]interface{}{
			"@context": tmpFile.Name(),
			"@type":    "TestType",
			"field1":   "value1",
		},
	}
	
	err = cache.validateReferencedObject(ctx, obj, 1*time.Hour, 30*time.Second, nil, false)
	assert.NoError(t, err)
}

func TestValidateReferencedObject_Invalid(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	
	schemaContent := `openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object
      additionalProperties: false
      x-jsonld:
        "@context": ./context.jsonld
        "@type": TestType
      properties:
        field1:
          type: string
      required:
        - field1`
	
	tmpFile.Write([]byte(schemaContent))
	tmpFile.Close()
	
	obj := referencedObject{
		Path:    "message.test",
		Context: tmpFile.Name(),
		Type:    "TestType",
		Data: map[string]interface{}{
			"@context": tmpFile.Name(),
			"@type":    "TestType",
		},
	}
	
	err = cache.validateReferencedObject(ctx, obj, 1*time.Hour, 30*time.Second, nil, false)
	assert.Error(t, err)
}

func TestValidateReferencedObject_DomainNotAllowed(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()
	
	obj := referencedObject{
		Path:    "message.test",
		Context: "https://malicious.com/schema.yaml",
		Type:    "TestType",
		Data:    map[string]interface{}{},
	}
	
	allowedDomains := []string{"trusted.com"}
	
	err := cache.validateReferencedObject(ctx, obj, 1*time.Hour, 30*time.Second, allowedDomains, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "domain not allowed")
}

func TestValidateExtendedSchemas_NoObjects(t *testing.T) {
	v := &schemav2Validator{
		config: &Config{
			EnableExtendedSchema: true,
			ExtendedSchemaConfig: ExtendedSchemaConfig{},
		},
		schemaCache: newSchemaCache(10),
	}
	
	ctx := context.Background()
	body := map[string]interface{}{
		"message": map[string]interface{}{
			"field": "value",
		},
	}
	
	err := v.validateExtendedSchemas(ctx, body)
	assert.NoError(t, err)
}

func TestValidateExtendedSchemas_MissingMessage(t *testing.T) {
	v := &schemav2Validator{
		config: &Config{
			EnableExtendedSchema: true,
		},
		schemaCache: newSchemaCache(10),
	}
	
	ctx := context.Background()
	body := map[string]interface{}{
		"context": map[string]interface{}{},
	}
	
	err := v.validateExtendedSchemas(ctx, body)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing 'message' field")
}

func TestIsSchemaVersionSegment(t *testing.T) {
	tests := []struct {
		name string
		seg  string
		want bool
	}{
		{name: "v1", seg: "v1", want: true},
		{name: "v2.1", seg: "v2.1", want: true},
		{name: "v1.2.3", seg: "v1.2.3", want: true},
		{name: "V2 uppercase", seg: "V2", want: true},
		{name: "1.0 no prefix", seg: "1.0", want: true},
		{name: "bare v", seg: "v", want: false},
		{name: "type name", seg: "JobType", want: false},
		{name: "empty", seg: "", want: false},
		{name: "v1beta", seg: "v1beta", want: false},
		{name: "plain word", seg: "main", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isSchemaVersionSegment(tt.seg))
		})
	}
}

func TestExtractRelativeSchemaPath(t *testing.T) {
	tests := []struct {
		name string
		rawURL string
		want string
	}{
		{
			name:   "URL with /schema/ marker and version",
			rawURL: "https://example.com/schema/JobType/v2.1/attributes.yaml",
			want:   "JobType/attributes.yaml",
		},
		{
			name:   "namespaced URL extracts last non-version segment",
			rawURL: "https://example.com/schema/common/CodedValue/v2.1/attributes.yaml",
			want:   "CodedValue/attributes.yaml",
		},
		{
			name:   "GitHub raw URL without /schema/ marker",
			rawURL: "https://raw.githubusercontent.com/org/repo/main/hiring-jobs/HiringJobResource/v2.1/attributes.yaml",
			want:   "HiringJobResource/attributes.yaml",
		},
		{
			name:   "context.jsonld URL",
			rawURL: "https://example.com/schema/ChargingSession/v1/context.jsonld",
			want:   "ChargingSession/attributes.yaml",
		},
		{
			name:   "no version segment",
			rawURL: "https://example.com/schema/JobType/attributes.yaml",
			want:   "JobType/attributes.yaml",
		},
		{
			name:   "bare key no scheme",
			rawURL: "JobType/attributes.yaml",
			want:   "JobType/attributes.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, extractRelativeSchemaPath(u))
		})
	}
}

func TestRawSchemaKey(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		want string
	}{
		{
			name: "2-part path unchanged",
			rel:  "JobType/attributes.yaml",
			want: "JobType/attributes.yaml",
		},
		{
			name: "3-part path strips version segment",
			rel:  "JobType/v2.1/attributes.yaml",
			want: "JobType/attributes.yaml",
		},
		{
			name: "3-part path with v1.0",
			rel:  "AnotherType/v1.0/attributes.yaml",
			want: "AnotherType/attributes.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, rawSchemaKey(tt.rel))
		})
	}
}

func TestRawSchemaKey_ConsistentWithExtractRelativeSchemaPath(t *testing.T) {
	cases := []struct {
		fileRel   string
		schemaURL string
	}{
		{
			fileRel:   "JobType/v2.1/attributes.yaml",
			schemaURL: "https://example.com/schema/JobType/v2.1/attributes.yaml",
		},
		{
			fileRel:   "HiringJobResource/v2.1/attributes.yaml",
			schemaURL: "https://raw.githubusercontent.com/org/repo/main/hiring-jobs/HiringJobResource/v2.1/attributes.yaml",
		},
	}
	for _, c := range cases {
		fileKey := rawSchemaKey(c.fileRel)
		u, _ := url.Parse(c.schemaURL)
		urlKey := extractRelativeSchemaPath(u)
		assert.Equal(t, fileKey, urlKey, "key mismatch for %s", c.fileRel)
	}
}

func TestPreloadSchemasToCache(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "schema-test-*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	schemaContent := []byte(`openapi: 3.1.0
info:
  title: Test
  version: 1.0.0`)

	os.MkdirAll(filepath.Join(tmpDir, "TypeA", "v2.1"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "TypeA", "v2.1", "attributes.yaml"), schemaContent, 0644)

	os.MkdirAll(filepath.Join(tmpDir, "TypeB", "v1.0"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "TypeB", "v1.0", "attributes.yaml"), schemaContent, 0644)

	cache := newSchemaCache(10)
	ctx := context.Background()

	err = preloadSchemasToCache(ctx, cache, tmpDir)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(cache.rawSchemas))
	assert.Contains(t, cache.rawSchemas, "TypeA/attributes.yaml")
	assert.Contains(t, cache.rawSchemas, "TypeB/attributes.yaml")
}

func TestPreloadSchemasToCache_InvalidDir(t *testing.T) {
	cache := newSchemaCache(10)
	err := preloadSchemasToCache(context.Background(), cache, "/nonexistent/dir")
	assert.Error(t, err)
}

func TestLoadSchemaFromPath_RawSchemasHit(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()

	schemaContent := `openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object`

	cache.rawSchemas["TestType/attributes.yaml"] = []byte(schemaContent)

	doc, err := cache.loadSchemaFromPath(ctx, "TestType/attributes.yaml", 1*time.Hour, 30*time.Second, true)
	assert.NoError(t, err)
	assert.NotNil(t, doc)
	assert.Equal(t, "3.1.0", doc.OpenAPI)
}

func TestLoadSchemaFromPath_LRUHit(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()

	expected := &openapi3.T{OpenAPI: "3.1.0"}
	cache.set(hashURL("TestType/attributes.yaml"), expected, 1*time.Hour)

	// localSchema=false skips rawSchemas step, goes straight to LRU
	doc, err := cache.loadSchemaFromPath(ctx, "TestType/attributes.yaml", 1*time.Hour, 30*time.Second, false)
	assert.NoError(t, err)
	assert.Equal(t, expected, doc)
}

func TestLoadSchemaFromPath_LocalMissFallsBackToFile(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()

	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	tmpFile.Write([]byte(`openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0`))
	tmpFile.Close()

	// rawSchemas empty, localSchema=true — local miss, falls through to file load
	doc, err := cache.loadSchemaFromPath(ctx, tmpFile.Name(), 1*time.Hour, 30*time.Second, true)
	assert.NoError(t, err)
	assert.NotNil(t, doc)
}

func TestValidateReferencedObject_LocalSchemaHit(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()

	cache.rawSchemas["TestType/attributes.yaml"] = []byte(`openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object
      properties:
        field1:
          type: string`)

	obj := referencedObject{
		Path: "message.test",
		Type: "TestType",
		Data: map[string]interface{}{
			"@type":  "TestType",
			"field1": "value1",
		},
	}

	// localSchema=true, no @context — relies entirely on rawSchemas lookup by @type
	err := cache.validateReferencedObject(ctx, obj, 1*time.Hour, 30*time.Second, nil, true)
	assert.NoError(t, err)
}

func TestValidateReferencedObject_LocalMissFallsBackToContext(t *testing.T) {
	cache := newSchemaCache(10)
	ctx := context.Background()

	tmpFile, err := os.CreateTemp("", "test-schema-*.yaml")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	tmpFile.Write([]byte(`openapi: 3.1.0
info:
  title: Test Schema
  version: 1.0.0
components:
  schemas:
    TestType:
      type: object
      properties:
        field1:
          type: string`))
	tmpFile.Close()

	obj := referencedObject{
		Path:    "message.test",
		Context: tmpFile.Name(),
		Type:    "TestType",
		Data: map[string]interface{}{
			"@context": tmpFile.Name(),
			"@type":    "TestType",
			"field1":   "value1",
		},
	}

	// rawSchemas empty, localSchema=true — local miss, falls back to @context file path
	err = cache.validateReferencedObject(ctx, obj, 1*time.Hour, 30*time.Second, nil, true)
	assert.NoError(t, err)
}
