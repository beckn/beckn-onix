package schemaversionmediator

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"gopkg.in/yaml.v3"
)

// observedSeeder appends schema objects seen in live traffic to a local
// node-manifest.yaml file. A nil *observedSeeder disables seeding.
type observedSeeder struct {
	path string

	mu     sync.Mutex
	cached *model.NodeManifest
}

// newObservedSeeder returns an observedSeeder for path, or nil if path is empty.
func newObservedSeeder(path string) *observedSeeder {
	if path == "" {
		return nil
	}
	return &observedSeeder{path: path}
}

// observe appends any schema object type or version in refs not yet declared
// in the manifest, then atomically rewrites the file. Malformed refs are
// skipped. The load-diff-write sequence is mutex-guarded so concurrent
// requests cannot race on the file.
func (s *observedSeeder) observe(refs []SchemaObjectRef) error {
	if s == nil || len(refs) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	nm, err := s.load()
	if err != nil {
		return err
	}

	typeIndex := make(map[string]int, len(nm.Schema.SchemaObjects))
	for i, obj := range nm.Schema.SchemaObjects {
		typeIndex[obj.Type] = i
	}

	changed := false
	for _, ref := range refs {
		typeName, version, baseURL, ok := parseObservedRef(ref)
		if !ok {
			continue
		}
		if i, known := typeIndex[typeName]; known {
			obj := &nm.Schema.SchemaObjects[i]
			if !containsVersion(obj.SupportedVersions, version) {
				obj.SupportedVersions = append(obj.SupportedVersions, version)
				changed = true
			}
			continue
		}
		nm.Schema.SchemaObjects = append(nm.Schema.SchemaObjects, model.SchemaObject{
			Type:              typeName,
			BaseURL:           baseURL,
			SupportedVersions: []string{version},
		})
		typeIndex[typeName] = len(nm.Schema.SchemaObjects) - 1
		changed = true
	}

	if !changed {
		return nil
	}
	return s.writeAndCache(nm)
}

// load returns the cached manifest, reading the file on first use.
func (s *observedSeeder) load() (*model.NodeManifest, error) {
	if s.cached != nil {
		return s.cached, nil
	}
	content, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: read manifest %q: %w", s.path, err)
	}
	nm, err := model.ParseNodeManifest(content)
	if err != nil {
		return nil, fmt.Errorf("schemaversionmediator: parse manifest %q: %w", s.path, err)
	}
	s.cached = nm
	return nm, nil
}

// writeAndCache writes nm to a temp file in the same directory and renames
// it over path, so readers never see a partial write.
func (s *observedSeeder) writeAndCache(nm *model.NodeManifest) error {
	out, err := yaml.Marshal(nm)
	if err != nil {
		return fmt.Errorf("schemaversionmediator: marshal manifest: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".node-manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("schemaversionmediator: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("schemaversionmediator: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("schemaversionmediator: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("schemaversionmediator: rename temp file to %q: %w", s.path, err)
	}

	s.cached = nm
	return nil
}

// parseObservedRef extracts the normalized type, version, and base URL from
// ref. ok is false if @context isn't a well-formed absolute URL, @type is
// empty, or no version segment can be found.
func parseObservedRef(ref SchemaObjectRef) (typeName, version, baseURL string, ok bool) {
	typeName = ref.Type
	if idx := strings.LastIndex(typeName, ":"); idx >= 0 {
		typeName = typeName[idx+1:]
	}
	if typeName == "" {
		return "", "", "", false
	}

	u, err := url.Parse(ref.ContextURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", "", false
	}

	version, err = extractVersionSegment(ref.ContextURL)
	if err != nil {
		return "", "", "", false
	}

	idx := strings.LastIndex(ref.ContextURL, "/"+version+"/")
	if idx < 0 {
		return "", "", "", false
	}
	baseURL = ref.ContextURL[:idx]
	return typeName, version, baseURL, true
}
