package main

import "runtime/debug"

// Version identification.
//
// Releases set the exact version from the git tag at build time:
//
//	go build -ldflags "-X main.releaseVersion=2.6.0"
//
// Any other build (go build, go install, CI, a hand-built node binary)
// reports "<nextVersion>-dev+<commit>[.dirty]". A dev build therefore sorts
// above the previous release and below the next one, and list-targets, the
// update nudge and `version` can tell it apart from a real release.
// Bump nextVersion right after tagging a release.
const nextVersion = "2.6.0"

// releaseVersion is set by -ldflags -X for release builds; empty otherwise.
// It must stay a var: -X silently does nothing to a const.
var releaseVersion = ""

// Version is the version this binary reports everywhere.
var Version = resolveVersion(releaseVersion, readBuildVCS)

// readBuildVCS returns the commit and dirty flag Go embeds at build time
// (-buildvcs, on by default inside a git checkout).
func readBuildVCS() (revision string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return revision, modified
}

func resolveVersion(release string, vcs func() (string, bool)) string {
	if release != "" {
		return release
	}
	v := nextVersion + "-dev"
	if rev, modified := vcs(); rev != "" {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		v += "+" + rev
		if modified {
			v += ".dirty"
		}
	}
	return v
}
