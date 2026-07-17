// Package version holds build-time identity information for the adapter
// binary. Values are injected via -ldflags -X at build time (see
// install/setup.sh, Dockerfile.adapter, Dockerfile.adapter-with-plugins).
// When built without those flags (go run, go test, plain go build), they
// keep their zero-value defaults below.
package version

var (
	// Version is the nearest git tag at build time (e.g. v1.6.0), as
	// reported by `git describe --tags --always`.
	Version = "dev"

	// GitCommit is the short SHA of the commit the binary was built from.
	GitCommit = "unknown"

	// GitTreeState is "clean" if the working tree had no uncommitted
	// changes at build time, "dirty" otherwise, or "unknown" for builds
	// without -ldflags injection (go run, go test, plain go build).
	GitTreeState = "unknown"

	// BuildDate is the UTC build timestamp in RFC3339 format.
	BuildDate = "unknown"
)
