//go:build darwin || linux

package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/search"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/transfer"
)

func TestRuntimeCacheLimitsUseValidatedConfiguration(t *testing.T) {
	input := config.CacheConfig{GlobalBytes: 1024, GlobalEntries: 10, WorkspaceBytes: 512, MaxEvictionCandidates: 3}
	want := cache.Limits{GlobalBytes: 1024, GlobalEntries: 10, WorkspaceBytes: 512, MaxCandidates: 3}
	if got := runtimeCacheLimits(input); got != want {
		t.Fatalf("runtime cache limits = %#v, want %#v", got, want)
	}
}

func TestRuntimeTransferLimitsFreezeJobSemanticSettings(t *testing.T) {
	input := config.TransferConfig{
		MaxConcurrent: 2, MaxQueued: 16,
		GlobalBytesPerSecond: 1024, EndpointBytesPerSecond: 512, JobBytesPerSecond: 256,
	}
	concurrent, queued, policy := runtimeTransferLimits(input)
	if concurrent != 2 || queued != 16 {
		t.Fatalf("runtime transfer limits = %d/%d", concurrent, queued)
	}
	want := transfer.SchedulerPolicy{GlobalBytesPerSecond: 1024, EndpointBytesPerSecond: 512, JobBytesPerSecond: 256}
	if policy != want {
		t.Fatalf("runtime transfer policy = %#v, want %#v", policy, want)
	}
}

func TestRuntimePreviewLimitsUseValidatedConfiguration(t *testing.T) {
	input := config.PreviewConfig{
		MaxInputBytes: 100, MaxJSONBytes: 90, MaxJSONDepth: 8, MaxRenderedLines: 7,
		MaxOutputBytes: 80, MaxImagePixels: 70, MaxStyleSpans: 6,
		ImageMaxPayloadBytes: 50, ImageMaxOutputBytes: 60, ImageChunkBytes: 5, ImageMaxPixels: 40,
	}
	render, image := runtimePreviewLimits(input)
	wantRender := preview.Limits{
		MaxInputBytes: 100, MaxJSONBytes: 90, MaxJSONDepth: 8, MaxRenderedLines: 7,
		MaxOutputBytes: 80, MaxImagePixels: 70, MaxStyleSpans: 6,
	}
	wantImage := preview.ImageOutputLimits{MaxPayloadBytes: 50, MaxOutputBytes: 60, ChunkBytes: 5, MaxPixels: 40}
	if render != wantRender || image != wantImage {
		t.Fatalf("runtime preview limits = %#v / %#v, want %#v / %#v", render, image, wantRender, wantImage)
	}
}

func TestRuntimeSearchBudgetsUseValidatedConfiguration(t *testing.T) {
	input := config.SearchConfig{
		Filename: config.FilenameSearchConfig{
			PageItems: 8, EventBuffer: 7, ConcurrentLists: 1, MaxDepth: 6,
			MaxEntries: 5, MaxResults: 4, MaxOutputBytes: 3, MaxDurationMS: 2,
		},
		Content: config.ContentSearchConfig{
			PageItems: 18, EventBuffer: 17, MaxDepth: 16, MaxEntries: 15,
			MaxFiles: 14, MaxResults: 13, MaxMatchesPerFile: 12, MaxFileBytes: 11,
			MaxReadBytes: 11, MaxSnippetBytes: 10, MaxOutputBytes: 9, MaxDurationMS: 8,
		},
	}
	filename, content := runtimeSearchBudgets(input)
	wantFilename := search.Budget{
		PageItems: 8, EventBuffer: 7, ConcurrentLists: 1, MaxDepth: 6,
		MaxEntries: 5, MaxResults: 4, MaxOutputBytes: 3, MaxDuration: 2 * time.Millisecond,
	}
	wantContent := search.ContentBudget{
		PageItems: 18, EventBuffer: 17, MaxDepth: 16, MaxEntries: 15,
		MaxFiles: 14, MaxResults: 13, MaxMatchesPerFile: 12, MaxFileBytes: 11,
		MaxReadBytes: 11, MaxSnippetBytes: 10, MaxOutputBytes: 9, MaxDuration: 8 * time.Millisecond,
	}
	if filename != wantFilename || content != wantContent {
		t.Fatalf("runtime search budgets = %#v / %#v, want %#v / %#v", filename, content, wantFilename, wantContent)
	}
}

func TestRuntimeRetrySettingsUseValidatedConfiguration(t *testing.T) {
	policy, jobDelay := runtimeRetrySettings(config.RetryConfig{
		ReconnectDelaysMS: []int64{200, 400}, JobRetryDelayMS: 120_000,
	})
	if want := []time.Duration{200 * time.Millisecond, 400 * time.Millisecond}; !reflect.DeepEqual(policy.Delays, want) {
		t.Fatalf("runtime reconnect delays = %v, want %v", policy.Delays, want)
	}
	if policy.Sleep == nil || policy.Jitter == nil {
		t.Fatal("runtime reconnect policy omitted sleep or jitter behavior")
	}
	if jobDelay != 2*time.Minute {
		t.Fatalf("runtime Job retry delay = %v, want %v", jobDelay, 2*time.Minute)
	}
}
