package version

import (
	"runtime/debug"
	"testing"
)

func TestDescribe(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "1234567890123456"}, {Key: "vcs.modified", Value: "true"}}}
	if got := describe(info); got != "dev+123456789012.dirty" {
		t.Fatal(got)
	}
	info.Main.Version = "v0.1.0"
	if got := describe(info); got != "v0.1.0" {
		t.Fatal(got)
	}
}
