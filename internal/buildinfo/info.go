package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	version = "dev"
	commit  = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Dirty     bool   `json:"dirty"`
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
}

func Current() Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		GoVersion: runtime.Version(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
	}

	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			if info.Commit == "unknown" && setting.Value != "" {
				info.Commit = setting.Value
			}
		case "vcs.modified":
			info.Dirty = setting.Value == "true"
		}
	}

	return info
}

func (i Info) String() string {
	return fmt.Sprintf(
		"%s commit=%s dirty=%t go=%s os=%s arch=%s",
		i.Version,
		i.Commit,
		i.Dirty,
		i.GoVersion,
		i.GOOS,
		i.GOARCH,
	)
}
