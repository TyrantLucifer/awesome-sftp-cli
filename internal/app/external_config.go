//go:build darwin || linux

package app

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/edit"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalpreviewer"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/externalprocess"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
	"golang.org/x/sys/unix"
)

func runtimeCacheLimits(input config.CacheConfig) cache.Limits {
	return cache.Limits{
		GlobalBytes: input.GlobalBytes, GlobalEntries: input.GlobalEntries,
		WorkspaceBytes: input.WorkspaceBytes, MaxCandidates: input.MaxEvictionCandidates,
	}
}

func runtimeDiagnosticConfig(input config.DiagnosticConfig) diagnostic.Config {
	return diagnostic.Config{
		MaxBytes: input.LogMaxBytes, Backups: input.LogBackups, RingCapacity: input.RingRecords,
	}
}

func runtimeTransferLimits(input config.TransferConfig) (int, int, transfer.SchedulerPolicy) {
	return input.MaxConcurrent, input.MaxQueued, transfer.SchedulerPolicy{
		GlobalBytesPerSecond: input.GlobalBytesPerSecond, EndpointBytesPerSecond: input.EndpointBytesPerSecond,
		JobBytesPerSecond: input.JobBytesPerSecond,
	}
}

func runtimePreviewLimits(input config.PreviewConfig) (preview.Limits, preview.ImageOutputLimits) {
	return preview.Limits{
			MaxInputBytes: input.MaxInputBytes, MaxJSONBytes: input.MaxJSONBytes, MaxJSONDepth: input.MaxJSONDepth,
			MaxRenderedLines: input.MaxRenderedLines, MaxOutputBytes: input.MaxOutputBytes,
			MaxImagePixels: input.MaxImagePixels, MaxStyleSpans: input.MaxStyleSpans,
		}, preview.ImageOutputLimits{
			MaxPayloadBytes: input.ImageMaxPayloadBytes, MaxOutputBytes: input.ImageMaxOutputBytes,
			ChunkBytes: input.ImageChunkBytes, MaxPixels: input.ImageMaxPixels,
		}
}

func runtimeSearchBudgets(input config.SearchConfig) (search.Budget, search.ContentBudget) {
	return search.Budget{
			PageItems: input.Filename.PageItems, EventBuffer: input.Filename.EventBuffer,
			ConcurrentLists: input.Filename.ConcurrentLists, MaxDepth: input.Filename.MaxDepth,
			MaxEntries: input.Filename.MaxEntries, MaxResults: input.Filename.MaxResults,
			MaxOutputBytes: input.Filename.MaxOutputBytes,
			MaxDuration:    time.Duration(input.Filename.MaxDurationMS) * time.Millisecond,
		}, search.ContentBudget{
			PageItems: input.Content.PageItems, EventBuffer: input.Content.EventBuffer,
			MaxDepth: input.Content.MaxDepth, MaxEntries: input.Content.MaxEntries,
			MaxFiles: input.Content.MaxFiles, MaxResults: input.Content.MaxResults,
			MaxMatchesPerFile: input.Content.MaxMatchesPerFile, MaxFileBytes: input.Content.MaxFileBytes,
			MaxReadBytes: input.Content.MaxReadBytes, MaxSnippetBytes: input.Content.MaxSnippetBytes,
			MaxOutputBytes: input.Content.MaxOutputBytes,
			MaxDuration:    time.Duration(input.Content.MaxDurationMS) * time.Millisecond,
		}
}

func runtimeRetrySettings(input config.RetryConfig) (reconnectPolicy, time.Duration) {
	delays := make([]time.Duration, len(input.ReconnectDelaysMS))
	for index, milliseconds := range input.ReconnectDelaysMS {
		delays[index] = time.Duration(milliseconds) * time.Millisecond
	}
	return newReconnectPolicy(delays), time.Duration(input.JobRetryDelayMS) * time.Millisecond
}

func runtimeDirectPolicy(integrity config.IntegrityConfig, direct config.DirectTransferConfig) transfer.DirectPolicy {
	return transfer.DirectPolicy{
		UserEnabled: direct.Enabled,
		Integrity:   transfer.IntegrityPolicy(integrity.TransferPolicy),
	}
}

type externalRuntimeConfig struct {
	editor    externalprocess.ResolvedCommand
	editorErr error
	opener    externalprocess.ResolvedCommand
	openerErr error
	previewer *externalpreviewer.Runner
}

func loadApplicationConfig(path string) (config.Config, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return config.Default(), nil
	} else if err != nil {
		return config.Config{}, fmt.Errorf("inspect config %q: %w", path, err)
	}
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		return config.Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return config.Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	file := os.NewFile(uintptr(descriptor), path)
	if file == nil {
		_ = unix.Close(descriptor)
		return config.Config{}, fmt.Errorf("open config %q: invalid descriptor", path)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return config.Config{}, fmt.Errorf("open config %q: invalid regular file", path)
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(opened, current) {
		return config.Config{}, fmt.Errorf("open config %q: file identity changed", path)
	}
	decoded, err := config.Decode(file)
	if err != nil {
		return config.Config{}, fmt.Errorf("config %q: %w", path, err)
	}
	return decoded, nil
}

func resolveExternalRuntimeConfig(input config.ExternalConfig, environment []string) (externalRuntimeConfig, error) {
	environmentMap := environmentValues(environment)
	pathEnvironment := environmentMap["PATH"]
	editor, editorErr := externalprocess.ResolveEditor(commandConfig(input.Editor), environmentMap, pathEnvironment)
	opener, openerErr := externalprocess.ResolveOpener(commandConfig(input.Opener), runtime.GOOS, pathEnvironment)
	result := externalRuntimeConfig{editor: editor, editorErr: editorErr, opener: opener, openerErr: openerErr}
	if len(input.Previewers) == 0 {
		return result, nil
	}
	rules := make([]externalpreviewer.Rule, 0, len(input.Previewers))
	for index, item := range input.Previewers {
		command, err := externalprocess.ResolveCommand(externalprocess.Command{Executable: item.Command.Executable, Args: append([]string(nil), item.Command.Args...)}, pathEnvironment)
		if err != nil {
			return externalRuntimeConfig{}, fmt.Errorf("resolve external previewer %d: %w", index, err)
		}
		rules = append(rules, externalpreviewer.Rule{
			Name: item.Name, Match: externalpreviewer.Match{MediaTypes: append([]string(nil), item.MediaTypes...), Extensions: append([]string(nil), item.Extensions...)},
			Command: command, Timeout: time.Duration(item.TimeoutMS) * time.Millisecond, MaxInputBytes: item.MaxInputBytes, RequireComplete: item.RequireComplete,
		})
	}
	runner, err := externalpreviewer.New(rules, environment)
	if err != nil {
		return externalRuntimeConfig{}, err
	}
	result.previewer = runner
	return result, nil
}

func (runtime externalRuntimeConfig) command(purpose edit.Purpose) (externalprocess.ResolvedCommand, error) {
	switch purpose {
	case edit.PurposeEditor:
		return runtime.editor, runtime.editorErr
	case edit.PurposeOpener:
		return runtime.opener, runtime.openerErr
	default:
		return externalprocess.ResolvedCommand{}, fmt.Errorf("resolve external action: unsupported purpose %q", purpose)
	}
}

func commandConfig(input *config.CommandConfig) *externalprocess.Command {
	if input == nil {
		return nil
	}
	return &externalprocess.Command{Executable: input.Executable, Args: append([]string(nil), input.Args...)}
}

func environmentValues(environment []string) map[string]string {
	result := make(map[string]string)
	for _, entry := range environment {
		for index := range entry {
			if entry[index] == '=' {
				result[entry[:index]] = entry[index+1:]
				break
			}
		}
	}
	return result
}
