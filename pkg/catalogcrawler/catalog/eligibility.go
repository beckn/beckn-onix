package catalog

// Select decides whether the crawler should carry this catalog and, if so, the
// visibleTo network set to hand to Discovery.
//
// A public catalog (no networkIds) is always taken and is visible to everyone
// (visibleTo nil). A network-scoped catalog is taken only if its networks
// intersect the crawler's configured (or /crawl-requested) networkIds, and its
// own networks flow through as visibleTo.
func Select(entry CatalogEntry, crawlerNetworks []string) (take bool, visibleTo []string) {
	if entry.IsPublic() {
		return true, nil
	}
	want := make(map[string]bool, len(crawlerNetworks))
	for _, n := range crawlerNetworks {
		want[n] = true
	}
	for _, n := range entry.NetworkIDs {
		if want[n] {
			return true, entry.NetworkIDs
		}
	}
	return false, nil
}
