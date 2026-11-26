package version

import (
	"runtime/debug"
	"strings"
)

func Current() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return describe(info)
}

func describe(info *debug.BuildInfo) string {
	if info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	revision, dirty := "", false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	result := "dev"
	if revision != "" {
		result += "+" + strings.TrimSpace(revision)
	}
	if dirty {
		result += ".dirty"
	}
	return result
}
