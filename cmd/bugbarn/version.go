package main

// resolveVersion returns the release the process reports. The deploy states
// the release in BUGBARN_VERSION: images are built once per commit and retagged
// to the semver afterwards, so the binary cannot know its own semver. Without
// the override the build-time value stands (a dev build, or a release binary).
func resolveVersion(build, override string) string {
	if override != "" {
		return override
	}
	return build
}
