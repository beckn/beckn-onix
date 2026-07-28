package catalog

// IsPublic reports whether the catalog is visible to everyone — no networkIds
// scope it (File Specifications: "empty or absent means public").
func (e CatalogEntry) IsPublic() bool { return len(e.NetworkIDs) == 0 }
