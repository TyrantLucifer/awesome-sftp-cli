//go:build darwin || linux

package app

import (
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/cache"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/config"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/preview"
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
