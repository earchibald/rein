package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
)

var (
	// Version is the user-visible CalVer or dev version stamped at build time.
	Version = "dev"
	// Commit is the source revision stamped at build time.
	Commit = ""
	// BuildTime is the UTC build timestamp stamped at build time.
	BuildTime = ""
	// BuiltBy identifies the build system that produced the binary.
	BuiltBy = ""
)

// Info captures the user-visible build provenance embedded in the CLI.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildTime string `json:"buildTime,omitempty"`
	BuiltBy   string `json:"builtBy,omitempty"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
	Modified  bool   `json:"modified"`
}

// Current returns the best available build provenance for the running binary.
func Current() Info {
	return infoFromSettings(Version, Commit, BuildTime, BuiltBy, readBuildSettings())
}

func infoFromSettings(version, commit, buildTime, builtBy string, settings map[string]string) Info {
	info := Info{
		Version:   normalize(version, "dev"),
		Commit:    normalize(commit, settings["vcs.revision"]),
		BuildTime: normalize(buildTime, settings["vcs.time"]),
		BuiltBy:   normalize(builtBy, "local"),
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		Modified:  settings["vcs.modified"] == "true",
	}
	return info
}

func normalize(primary, fallback string) string {
	primary = strings.TrimSpace(primary)
	if primary != "" {
		return primary
	}
	return strings.TrimSpace(fallback)
}

func readBuildSettings() map[string]string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}

	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings
}
