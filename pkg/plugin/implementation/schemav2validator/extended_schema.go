package schemav2validator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/getkin/kin-openapi/openapi3"
)

// ExtendedSchemaConfig holds configuration for referenced schema validation.
type ExtendedSchemaConfig struct {
	CacheTTL        int      // seconds, default 86400 (24h)
	MaxCacheSize    int      // default 100
	DownloadTimeout int      // seconds, default 30
	AllowedDomains  []string // whitelist (empty = all allowed)
}

// referencedObject represents a domain-specific object with @context.
type referencedObject struct {
	Path    string
	Context string
	Type    string
	Data    map[string]interface{}
}

// schemaCache caches loaded domain schemas with LRU eviction.
type schemaCache struct {
	mu      sync.RWMutex
	schemas map[string]*cachedDomainSchema
	maxSize int
}

// cachedDomainSchema holds a cached domain schema with metadata.
type cachedDomainSchema struct {
	doc          *openapi3.T
	loadedAt     time.Time
	expiresAt    time.Time
	lastAccessed time.Time
	accessCount  int64
}

// isCoreSchema checks if @context URL is a core Beckn schema.
func isCoreSchema(contextURL string) bool {
	return strings.Contains(contextURL, "/schema/core/")
}

// validateExtendedSchemas validates all objects with @context against their schemas.
func (v *schemav2Validator) validateExtendedSchemas(ctx context.Context, body interface{}) error {
	// Extract "message" object - scan inside message
	bodyMap, ok := body.(map[string]interface{})
	if !ok {
		return fmt.Errorf("body is not a valid JSON object")
	}

	message, hasMessage := bodyMap["message"]
	if !hasMessage {
		return fmt.Errorf("missing 'message' field in request body")
	}

	// Find domain-specific objects with @context (skip core schemas)
	objects := findReferencedObjects(message, "message")

	if len(objects) == 0 {
		log.Debugf(ctx, "No domain-specific objects with @context found, skipping Extended Schema validation")
		return nil
	}

	log.Debugf(ctx, "Found %d domain-specific objects with @context for Extended Schema validation", len(objects))

	// Get config with defaults
	ttl := 86400 * time.Second // 24 hours default
	timeout := 30 * time.Second
	var allowedDomains []string

	refConfig := v.config.ExtendedSchemaConfig
	if refConfig.CacheTTL > 0 {
		ttl = time.Duration(refConfig.CacheTTL) * time.Second
	}
	if refConfig.DownloadTimeout > 0 {
		timeout = time.Duration(refConfig.DownloadTimeout) * time.Second
	}
	allowedDomains = refConfig.AllowedDomains

	log.Debugf(ctx, "Extended Schema config: ttl=%v, timeout=%v, allowedDomains=%v",
		ttl, timeout, allowedDomains)

	// Validate each object
	for _, obj := range objects {
		log.Debugf(ctx, "Validating object at path: %s, @context: %s, @type: %s",
			obj.Path, obj.Context, obj.Type)

		if err := v.schemaCache.validateReferencedObject(ctx, obj, ttl, timeout, allowedDomains); err != nil {
			// Extract and prefix error paths
			var schemaErrors []model.Error
			v.extractSchemaErrors(err, &schemaErrors)

			// Prefix all paths with object path
			for i := range schemaErrors {
				if schemaErrors[i].Paths != "" {
					schemaErrors[i].Paths = obj.Path + "." + schemaErrors[i].Paths
				} else {
					schemaErrors[i].Paths = obj.Path
				}
			}

			return &model.SchemaValidationErr{Errors: schemaErrors}
		}
	}

	return nil
}

// newSchemaCache creates a new schema cache.
func newSchemaCache(maxSize int) *schemaCache {
	return &schemaCache{
		schemas: make(map[string]*cachedDomainSchema),
		maxSize: maxSize,
	}
}

// hashURL creates a SHA256 hash of the URL for use as cache key.
func hashURL(urlStr string) string {
	hash := sha256.Sum256([]byte(urlStr))
	return hex.EncodeToString(hash[:])
}

// isValidSchemaPath validates if the schema path is safe to load.
func isValidSchemaPath(schemaPath string) bool {
	u, err := url.Parse(schemaPath)
	if err != nil {
		// Could be a simple file path
		return schemaPath != ""
	}
	// Support: http://, https://, file://, or no scheme (local path)
	return u.Scheme == "http" || u.Scheme == "https" ||
		u.Scheme == "file" || u.Scheme == ""
}

// get retrieves a cached schema and updates access tracking.
func (c *schemaCache) get(urlHash string) (*openapi3.T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cached, exists := c.schemas[urlHash]
	if !exists || time.Now().After(cached.expiresAt) {
		return nil, false
	}

	// Update access tracking
	cached.lastAccessed = time.Now()
	cached.accessCount++

	return cached.doc, true
}

// set stores a schema in the cache with TTL and LRU eviction.
func (c *schemaCache) set(urlHash string, doc *openapi3.T, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// LRU eviction if cache is full
	if len(c.schemas) >= c.maxSize {
		var oldest string
		var oldestTime time.Time
		for k, v := range c.schemas {
			if oldest == "" || v.lastAccessed.Before(oldestTime) {
				oldest, oldestTime = k, v.lastAccessed
			}
		}
		if oldest != "" {
			delete(c.schemas, oldest)
		}
	}

	c.schemas[urlHash] = &cachedDomainSchema{
		doc:          doc,
		loadedAt:     time.Now(),
		expiresAt:    time.Now().Add(ttl),
		lastAccessed: time.Now(),
		accessCount:  1,
	}
}

// cleanupExpired removes expired schemas from cache.
func (c *schemaCache) cleanupExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expired := make([]string, 0)

	for urlHash, cached := range c.schemas {
		if now.After(cached.expiresAt) {
			expired = append(expired, urlHash)
		}
	}

	for _, urlHash := range expired {
		delete(c.schemas, urlHash)
	}

	return len(expired)
}

// loadSchemaFromPath loads a schema from URL or local file with timeout and caching.
func (c *schemaCache) loadSchemaFromPath(ctx context.Context, schemaPath string, ttl, timeout time.Duration) (*openapi3.T, error) {
	urlHash := hashURL(schemaPath)

	// Check cache first
	if doc, found := c.get(urlHash); found {
		log.Debugf(ctx, "Schema cache hit for: %s", schemaPath)
		return doc, nil
	}

	log.Debugf(ctx, "Schema cache miss, loading from: %s", schemaPath)

	// Validate path format
	if !isValidSchemaPath(schemaPath) {
		return nil, fmt.Errorf("invalid schema path: %s", schemaPath)
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	var doc *openapi3.T
	var err error

	u, parseErr := url.Parse(schemaPath)
	if parseErr == nil && (u.Scheme == "http" || u.Scheme == "https") {
		// Load from URL with timeout
		loadCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		loader.Context = loadCtx
		doc, err = loader.LoadFromURI(u)
	} else {
		// Load from local file (file:// or path)
		filePath := schemaPath
		if u != nil && u.Scheme == "file" {
			filePath = u.Path
		}
		doc, err = loader.LoadFromFile(filePath)
	}

	if err != nil {
		log.Errorf(ctx, err, "Failed to load schema from: %s", schemaPath)
		return nil, fmt.Errorf("failed to load schema from %s: %w", schemaPath, err)
	}

	// Validate loaded schema (non-blocking, just log warnings)
	if err := doc.Validate(ctx); err != nil {
		log.Debugf(ctx, "Schema validation warnings for %s: %v", schemaPath, err)
	}

	c.set(urlHash, doc, ttl)
	log.Debugf(ctx, "Loaded and cached schema from: %s", schemaPath)

	return doc, nil
}

// findReferencedObjects recursively finds domain-specific objects with @context .
func findReferencedObjects(data interface{}, path string) []referencedObject {
	var results []referencedObject

	switch v := data.(type) {
	case map[string]interface{}:
		// Check for @context and @type
		if contextVal, hasContext := v["@context"].(string); hasContext {
			if typeVal, hasType := v["@type"].(string); hasType {
				// Skip core schemas during traversal
				if !isCoreSchema(contextVal) {
					results = append(results, referencedObject{
						Path:    path,
						Context: contextVal,
						Type:    typeVal,
						Data:    v,
					})
				}
			}
		}

		// Recurse into nested objects
		for key, val := range v {
			newPath := key
			if path != "" {
				newPath = path + "." + key
			}
			results = append(results, findReferencedObjects(val, newPath)...)
		}

	case []interface{}:
		// Recurse into arrays
		for i, item := range v {
			newPath := fmt.Sprintf("%s[%d]", path, i)
			results = append(results, findReferencedObjects(item, newPath)...)
		}
	}

	return results
}

// transformContextToSchemaURL transforms @context URL to schema URL.
func transformContextToSchemaURL(contextURL string) string {
	// transformation: context.jsonld -> attributes.yaml
	return strings.Replace(contextURL, "context.jsonld", "attributes.yaml", 1)
}

// findSchemaByType finds a schema in the document by @type value.
func findSchemaByType(ctx context.Context, doc *openapi3.T, typeName string) (*openapi3.SchemaRef, error) {
	if doc.Components == nil || doc.Components.Schemas == nil {
		return nil, fmt.Errorf("no schemas found in document")
	}

	// Try direct match by schema name
	if schema, exists := doc.Components.Schemas[typeName]; exists {
		log.Debugf(ctx, "Found schema by direct match: %s", typeName)
		return schema, nil
	}

	// Fallback: Try x-jsonld.@type match
	for name, schema := range doc.Components.Schemas {
		if schema.Value == nil {
			continue
		}
		if xJsonld, ok := schema.Value.Extensions["x-jsonld"].(map[string]interface{}); ok {
			if atType, ok := xJsonld["@type"].(string); ok && atType == typeName {
				log.Debugf(ctx, "Found schema by x-jsonld.@type match: %s (mapped to %s)", typeName, name)
				return schema, nil
			}
		}
	}

	return nil, fmt.Errorf("no schema found for @type: %s", typeName)
}

// isAllowedDomain checks if the URL domain is in the whitelist.
func isAllowedDomain(schemaURL string, allowedDomains []string) bool {
	if len(allowedDomains) == 0 {
		return true // No whitelist = all allowed
	}
	for _, domain := range allowedDomains {
		if strings.Contains(schemaURL, domain) {
			return true
		}
	}
	return false
}

// validateReferencedObject validates a single object with @context.
func (c *schemaCache) validateReferencedObject(
	ctx context.Context,
	obj referencedObject,
	ttl, timeout time.Duration,
	allowedDomains []string,
) error {
	// Domain whitelist check
	if !isAllowedDomain(obj.Context, allowedDomains) {
		log.Warnf(ctx, "Domain not in whitelist: %s", obj.Context)
		return fmt.Errorf("domain not allowed: %s", obj.Context)
	}

	// Transform @context to schema path (URL or file)
	schemaPath := transformContextToSchemaURL(obj.Context)
	log.Debugf(ctx, "Transformed %s -> %s", obj.Context, schemaPath)

	// Load schema with timeout (supports URL or local file)
	doc, err := c.loadSchemaFromPath(ctx, schemaPath, ttl, timeout)
	if err != nil {
		return fmt.Errorf("at %s: %w", obj.Path, err)
	}

	// Find schema by @type
	schema, err := findSchemaByType(ctx, doc, obj.Type)
	if err != nil {
		log.Errorf(ctx, err, "Schema not found for @type: %s at path: %s", obj.Type, obj.Path)
		return fmt.Errorf("at %s: %w", obj.Path, err)
	}

	// Strip JSON-LD metadata before validation
	domainData := make(map[string]interface{}, len(obj.Data)-2)
	for k, v := range obj.Data {
		if k != "@context" && k != "@type" {
			domainData[k] = v
		}
	}

	// Validate domain-specific data against schema
	opts := []openapi3.SchemaValidationOption{
		openapi3.VisitAsRequest(),
		openapi3.EnableFormatValidation(),
	}
	if err := schema.Value.VisitJSON(domainData, opts...); err != nil {
		log.Debugf(ctx, "Validation failed for @type: %s at path: %s: %v", obj.Type, obj.Path, err)
		return err
	}

	log.Debugf(ctx, "Validation passed for @type: %s at path: %s", obj.Type, obj.Path)
	return nil
}
