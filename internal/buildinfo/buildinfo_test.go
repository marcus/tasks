package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolvedVersionUsesPublicModuleVersionForGoInstall(t *testing.T) {
	info := &debug.BuildInfo{Main: debug.Module{Version: "v1.8.2"}}
	if got, want := resolvedVersion("dev", info), "v1.8.2"; got != want {
		t.Fatalf("resolved version = %q, want %q", got, want)
	}
}

func TestResolvedVersionPreservesLinkedAndDevelopmentVersions(t *testing.T) {
	for _, test := range []struct {
		name   string
		linked string
		info   *debug.BuildInfo
		want   string
	}{
		{name: "release ldflags win", linked: "v1.8.2", info: &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}}, want: "v1.8.2"},
		{name: "local build stays dev", linked: "dev", info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, want: "dev"},
		{name: "tagged local checkout stays dev", linked: "dev", info: &debug.BuildInfo{
			Main: debug.Module{Version: "v1.8.1+dirty"},
			Settings: []debug.BuildSetting{
				{Key: "vcs", Value: "git"},
				{Key: "vcs.modified", Value: "true"},
			},
		}, want: "dev"},
		{name: "missing build info stays dev", linked: "dev", want: "dev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := resolvedVersion(test.linked, test.info); got != test.want {
				t.Fatalf("resolved version = %q, want %q", got, test.want)
			}
		})
	}
}
