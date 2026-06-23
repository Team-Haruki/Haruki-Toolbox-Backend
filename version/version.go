package version

// These values are overridden at build time via -ldflags -X (see Dockerfile and
// the release workflow). They must be exported package variables for the linker
// to inject them; a missing -X target is silently dropped, which is why they are
// kept in sync with the build args that set them.
var (
	Version   = "v7.0.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)
