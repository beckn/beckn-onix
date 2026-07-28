package catalogcrawler

import (
	"fmt"
	"sort"

	"github.com/beckn-one/beckn-onix/pkg/catalogfile"
)

// FetchFunc fetches and verifies one file's bytes. The digest check lives
// inside the fetcher, which is injected so Resolve stays pure and testable.
type FetchFunc func(f FileEntry) ([]byte, error)

// Resolve builds the complete catalog at toVersion (the "Way B" model):
// fetch the baseline, then fold each change file up to toVersion (in
// version order) via catalogfile.Apply, returning the composed bytes. We
// keep no composed state locally, so a changed catalog is resolved in full.
func Resolve(entry CatalogEntry, toVersion int64, fetch FetchFunc) ([]byte, error) {
	current, err := fetch(entry.Baseline)
	if err != nil {
		return nil, fmt.Errorf("catalogcrawler: fetching baseline v%d: %w", entry.Baseline.Version, err)
	}

	changes := append([]FileEntry(nil), entry.Changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Version < changes[j].Version })

	for _, c := range changes {
		if c.URL == "" || c.Version <= entry.Baseline.Version || c.Version > toVersion {
			continue
		}
		b, err := fetch(c)
		if err != nil {
			return nil, fmt.Errorf("catalogcrawler: fetching change v%d: %w", c.Version, err)
		}
		current, err = catalogfile.Apply(current, b)
		if err != nil {
			return nil, fmt.Errorf("catalogcrawler: folding change v%d: %w", c.Version, err)
		}
	}
	return current, nil
}
