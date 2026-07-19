package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/keymap"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/preview"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/retrypolicy"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/search"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/transfer"
)

const (
	SchemaVersion = 1

	protocolMaxFrameBytes    uint32 = 8 * 1024 * 1024
	hardMaxPageSize          uint32 = 4096
	maxExternalCommands             = 32
	maxExternalArguments            = 128
	maxExternalItemBytes            = 4096
	maxExternalCommandBytes         = 32768
	maxExternalRuleNameBytes        = 128
	maxExternalMatchItems           = 64
	maxExternalTimeoutMS     int64  = 120_000
	maxExternalInputBytes    int64  = 1 << 30
	maxCacheGlobalBytes      int64  = 2 << 30
	maxCacheGlobalEntries           = 4096
	maxCacheWorkspaceBytes   int64  = 1 << 30
	maxCacheCandidates              = 256
	maxTransferConcurrent           = 4
	maxTransferQueued               = 128
	maxBandwidthBytesPerSec  uint64 = 1 << 40
)

type Config struct {
	SchemaVersion  int                  `json:"schema_version"`
	IPC            IPCConfig            `json:"ipc"`
	Listing        ListingConfig        `json:"listing"`
	Cache          CacheConfig          `json:"cache"`
	Transfer       TransferConfig       `json:"transfer"`
	Preview        PreviewConfig        `json:"preview"`
	Search         SearchConfig         `json:"search"`
	Retry          RetryConfig          `json:"retry"`
	Integrity      IntegrityConfig      `json:"integrity"`
	DirectTransfer DirectTransferConfig `json:"direct_transfer"`
	Diagnostic     DiagnosticConfig     `json:"diagnostic"`
	Helper         HelperConfig         `json:"helper"`
	External       ExternalConfig       `json:"external,omitempty"`
	Keymap         KeymapConfig         `json:"keymap,omitempty"`
}

type IPCConfig struct {
	MaxFrameBytes uint32 `json:"max_frame_bytes"`
}

type ListingConfig struct {
	DefaultPageSize uint32 `json:"default_page_size"`
	MaxPageSize     uint32 `json:"max_page_size"`
}

type CacheConfig struct {
	GlobalBytes           int64 `json:"global_bytes"`
	GlobalEntries         int   `json:"global_entries"`
	WorkspaceBytes        int64 `json:"workspace_bytes"`
	MaxEvictionCandidates int   `json:"max_eviction_candidates"`
}

type TransferConfig struct {
	MaxConcurrent          int    `json:"max_concurrent"`
	MaxQueued              int    `json:"max_queued"`
	GlobalBytesPerSecond   uint64 `json:"global_bytes_per_second"`
	EndpointBytesPerSecond uint64 `json:"endpoint_bytes_per_second"`
	JobBytesPerSecond      uint64 `json:"job_bytes_per_second"`
}

type PreviewConfig struct {
	MaxInputBytes        int    `json:"max_input_bytes"`
	MaxJSONBytes         int    `json:"max_json_bytes"`
	MaxJSONDepth         int    `json:"max_json_depth"`
	MaxRenderedLines     int    `json:"max_rendered_lines"`
	MaxOutputBytes       int    `json:"max_output_bytes"`
	MaxImagePixels       uint64 `json:"max_image_pixels"`
	MaxStyleSpans        int    `json:"max_style_spans"`
	ImageMaxPayloadBytes int    `json:"image_max_payload_bytes"`
	ImageMaxOutputBytes  int    `json:"image_max_output_bytes"`
	ImageChunkBytes      int    `json:"image_chunk_bytes"`
	ImageMaxPixels       uint64 `json:"image_max_pixels"`
}

type SearchConfig struct {
	Filename FilenameSearchConfig `json:"filename"`
	Content  ContentSearchConfig  `json:"content"`
}

type FilenameSearchConfig struct {
	PageItems       uint32 `json:"page_items"`
	EventBuffer     uint32 `json:"event_buffer"`
	ConcurrentLists uint32 `json:"concurrent_lists"`
	MaxDepth        uint32 `json:"max_depth"`
	MaxEntries      uint64 `json:"max_entries"`
	MaxResults      uint64 `json:"max_results"`
	MaxOutputBytes  uint64 `json:"max_output_bytes"`
	MaxDurationMS   int64  `json:"max_duration_ms"`
}

type ContentSearchConfig struct {
	PageItems         uint32 `json:"page_items"`
	EventBuffer       uint32 `json:"event_buffer"`
	MaxDepth          uint32 `json:"max_depth"`
	MaxEntries        uint64 `json:"max_entries"`
	MaxFiles          uint64 `json:"max_files"`
	MaxResults        uint64 `json:"max_results"`
	MaxMatchesPerFile uint64 `json:"max_matches_per_file"`
	MaxFileBytes      uint64 `json:"max_file_bytes"`
	MaxReadBytes      uint64 `json:"max_read_bytes"`
	MaxSnippetBytes   uint32 `json:"max_snippet_bytes"`
	MaxOutputBytes    uint64 `json:"max_output_bytes"`
	MaxDurationMS     int64  `json:"max_duration_ms"`
}

type RetryConfig struct {
	ReconnectDelaysMS []int64 `json:"reconnect_delays_ms"`
	JobRetryDelayMS   int64   `json:"job_retry_delay_ms"`
}

type IntegrityConfig struct {
	TransferPolicy string `json:"transfer_policy"`
}

type DirectTransferConfig struct {
	Enabled bool `json:"enabled"`
}

type DiagnosticConfig struct {
	LogMaxBytes int64 `json:"log_max_bytes"`
	LogBackups  int   `json:"log_backups"`
	RingRecords int   `json:"ring_records"`
}

type HelperConfig struct {
	Enabled bool `json:"enabled"`
}

type CommandConfig struct {
	Executable string   `json:"executable"`
	Args       []string `json:"argv"`
}

type ExternalConfig struct {
	Editor     *CommandConfig    `json:"editor,omitempty"`
	Opener     *CommandConfig    `json:"opener,omitempty"`
	Previewers []PreviewerConfig `json:"previewers,omitempty"`
}

type PreviewerConfig struct {
	Name            string        `json:"name"`
	MediaTypes      []string      `json:"media_types,omitempty"`
	Extensions      []string      `json:"extensions,omitempty"`
	Command         CommandConfig `json:"command"`
	TimeoutMS       int64         `json:"timeout_ms"`
	MaxInputBytes   int64         `json:"max_input_bytes"`
	RequireComplete bool          `json:"require_complete"`
}

type KeymapConfig struct {
	Bindings []keymap.Override `json:"bindings,omitempty"`
}

func Default() Config {
	diagnosticDefaults := diagnostic.DefaultConfig()
	renderLimits := preview.DefaultLimits()
	imageLimits := preview.DefaultImageOutputLimits()
	filenameBudget := search.DefaultFilenameBudget()
	contentBudget := search.DefaultContentBudget()
	reconnectDelays := retrypolicy.DefaultReconnectDelays()
	reconnectDelaysMS := make([]int64, len(reconnectDelays))
	for index, delay := range reconnectDelays {
		reconnectDelaysMS[index] = delay.Milliseconds()
	}
	return Config{
		SchemaVersion: SchemaVersion,
		IPC: IPCConfig{
			MaxFrameBytes: protocolMaxFrameBytes,
		},
		Listing: ListingConfig{
			DefaultPageSize: 256,
			MaxPageSize:     hardMaxPageSize,
		},
		Cache: CacheConfig{
			GlobalBytes: maxCacheGlobalBytes, GlobalEntries: maxCacheGlobalEntries,
			WorkspaceBytes: maxCacheWorkspaceBytes, MaxEvictionCandidates: maxCacheCandidates,
		},
		Transfer: TransferConfig{MaxConcurrent: maxTransferConcurrent, MaxQueued: maxTransferQueued},
		Preview: PreviewConfig{
			MaxInputBytes: renderLimits.MaxInputBytes, MaxJSONBytes: renderLimits.MaxJSONBytes,
			MaxJSONDepth: renderLimits.MaxJSONDepth, MaxRenderedLines: renderLimits.MaxRenderedLines,
			MaxOutputBytes: renderLimits.MaxOutputBytes, MaxImagePixels: renderLimits.MaxImagePixels,
			MaxStyleSpans: renderLimits.MaxStyleSpans, ImageMaxPayloadBytes: imageLimits.MaxPayloadBytes,
			ImageMaxOutputBytes: imageLimits.MaxOutputBytes, ImageChunkBytes: imageLimits.ChunkBytes,
			ImageMaxPixels: imageLimits.MaxPixels,
		},
		Search: SearchConfig{
			Filename: FilenameSearchConfig{
				PageItems: filenameBudget.PageItems, EventBuffer: filenameBudget.EventBuffer,
				ConcurrentLists: filenameBudget.ConcurrentLists, MaxDepth: filenameBudget.MaxDepth,
				MaxEntries: filenameBudget.MaxEntries, MaxResults: filenameBudget.MaxResults,
				MaxOutputBytes: filenameBudget.MaxOutputBytes,
				MaxDurationMS:  filenameBudget.MaxDuration.Milliseconds(),
			},
			Content: ContentSearchConfig{
				PageItems: contentBudget.PageItems, EventBuffer: contentBudget.EventBuffer,
				MaxDepth: contentBudget.MaxDepth, MaxEntries: contentBudget.MaxEntries,
				MaxFiles: contentBudget.MaxFiles, MaxResults: contentBudget.MaxResults,
				MaxMatchesPerFile: contentBudget.MaxMatchesPerFile, MaxFileBytes: contentBudget.MaxFileBytes,
				MaxReadBytes: contentBudget.MaxReadBytes, MaxSnippetBytes: contentBudget.MaxSnippetBytes,
				MaxOutputBytes: contentBudget.MaxOutputBytes,
				MaxDurationMS:  contentBudget.MaxDuration.Milliseconds(),
			},
		},
		Retry: RetryConfig{
			ReconnectDelaysMS: reconnectDelaysMS,
			JobRetryDelayMS:   retrypolicy.DefaultJobDelay.Milliseconds(),
		},
		Integrity:      IntegrityConfig{TransferPolicy: string(transfer.DefaultIntegrityPolicy())},
		DirectTransfer: DirectTransferConfig{Enabled: transfer.ProductionDirectTransferOpen},
		Diagnostic: DiagnosticConfig{
			LogMaxBytes: diagnosticDefaults.MaxBytes,
			LogBackups:  diagnosticDefaults.Backups,
			RingRecords: diagnosticDefaults.RingCapacity,
		},
		Helper: HelperConfig{Enabled: false},
	}
}

func Decode(r io.Reader) (Config, error) {
	decoder := json.NewDecoder(r)

	var document json.RawMessage
	if err := decoder.Decode(&document); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: trailing JSON value")
		}
		return Config{}, fmt.Errorf("decode config trailing data: %w", err)
	}

	var header struct {
		SchemaVersion *int `json:"schema_version"`
	}
	if err := json.Unmarshal(document, &header); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if header.SchemaVersion == nil {
		return Config{}, errors.New("decode config: schema_version is required")
	}

	config := Default()
	strict := json.NewDecoder(bytes.NewReader(document))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return config, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version %d is unsupported; want %d", c.SchemaVersion, SchemaVersion)
	}
	if c.IPC.MaxFrameBytes == 0 {
		return errors.New("ipc.max_frame_bytes must be greater than zero")
	}
	if c.IPC.MaxFrameBytes > protocolMaxFrameBytes {
		return fmt.Errorf("ipc.max_frame_bytes %d exceeds protocol maximum %d", c.IPC.MaxFrameBytes, protocolMaxFrameBytes)
	}
	if c.Listing.DefaultPageSize == 0 {
		return errors.New("listing.default_page_size must be greater than zero")
	}
	if c.Listing.MaxPageSize == 0 {
		return errors.New("listing.max_page_size must be greater than zero")
	}
	if c.Listing.MaxPageSize > hardMaxPageSize {
		return fmt.Errorf("listing.max_page_size %d exceeds hard maximum %d", c.Listing.MaxPageSize, hardMaxPageSize)
	}
	if c.Listing.DefaultPageSize > c.Listing.MaxPageSize {
		return fmt.Errorf(
			"listing.default_page_size %d exceeds listing.max_page_size %d",
			c.Listing.DefaultPageSize,
			c.Listing.MaxPageSize,
		)
	}
	if err := c.Cache.validate(); err != nil {
		return err
	}
	if err := c.Transfer.validate(); err != nil {
		return err
	}
	if err := c.Preview.validate(); err != nil {
		return err
	}
	if err := c.Search.validate(); err != nil {
		return err
	}
	if err := c.Retry.validate(); err != nil {
		return err
	}
	if c.Integrity.TransferPolicy != string(transfer.IntegrityStrong) &&
		c.Integrity.TransferPolicy != string(transfer.IntegrityRequireStrong) {
		return errors.New("integrity.transfer_policy must be strong or require_strong")
	}
	if c.DirectTransfer.Enabled != transfer.ProductionDirectTransferOpen {
		return errors.New("production direct transfer distribution is closed; direct_transfer.enabled must be false")
	}
	if err := c.Diagnostic.validate(); err != nil {
		return err
	}
	if c.Helper.Enabled && !helper.ProductionDistributionOpen {
		return errors.New("helper.enabled must be false while production distribution is closed")
	}
	if err := c.External.validate(); err != nil {
		return fmt.Errorf("external: %w", err)
	}
	if _, err := keymap.New(c.Keymap.Bindings); err != nil {
		return fmt.Errorf("keymap: %w", err)
	}
	return nil
}

func (c DiagnosticConfig) validate() error {
	maximum := diagnostic.DefaultConfig()
	if c.LogMaxBytes < 256 || c.LogMaxBytes > maximum.MaxBytes {
		return fmt.Errorf("diagnostic.log_max_bytes must be within 256..%d", maximum.MaxBytes)
	}
	if c.LogBackups < 1 || c.LogBackups > maximum.Backups {
		return fmt.Errorf("diagnostic.log_backups must be within 1..%d", maximum.Backups)
	}
	if c.RingRecords < 1 || c.RingRecords > maximum.RingCapacity {
		return fmt.Errorf("diagnostic.ring_records must be within 1..%d", maximum.RingCapacity)
	}
	return nil
}

func (c CacheConfig) validate() error {
	if c.GlobalBytes < 1 || c.GlobalBytes > maxCacheGlobalBytes {
		return fmt.Errorf("cache.global_bytes must be within 1..%d", maxCacheGlobalBytes)
	}
	if c.GlobalEntries < 1 || c.GlobalEntries > maxCacheGlobalEntries {
		return fmt.Errorf("cache.global_entries must be within 1..%d", maxCacheGlobalEntries)
	}
	if c.WorkspaceBytes < 1 || c.WorkspaceBytes > maxCacheWorkspaceBytes || c.WorkspaceBytes > c.GlobalBytes {
		return fmt.Errorf("cache.workspace_bytes must be within 1..min(cache.global_bytes,%d)", maxCacheWorkspaceBytes)
	}
	if c.MaxEvictionCandidates < 1 || c.MaxEvictionCandidates > maxCacheCandidates {
		return fmt.Errorf("cache.max_eviction_candidates must be within 1..%d", maxCacheCandidates)
	}
	return nil
}

func (c TransferConfig) validate() error {
	if c.MaxConcurrent < 1 || c.MaxConcurrent > maxTransferConcurrent {
		return fmt.Errorf("transfer.max_concurrent must be within 1..%d", maxTransferConcurrent)
	}
	if c.MaxQueued < c.MaxConcurrent || c.MaxQueued > maxTransferQueued {
		return fmt.Errorf("transfer.max_queued must be within transfer.max_concurrent..%d", maxTransferQueued)
	}
	for _, item := range []struct {
		name  string
		value uint64
	}{
		{name: "global_bytes_per_second", value: c.GlobalBytesPerSecond},
		{name: "endpoint_bytes_per_second", value: c.EndpointBytesPerSecond},
		{name: "job_bytes_per_second", value: c.JobBytesPerSecond},
	} {
		if item.value > maxBandwidthBytesPerSec {
			return fmt.Errorf("transfer.%s must be within 0..%d", item.name, maxBandwidthBytesPerSec)
		}
	}
	return nil
}

func (c PreviewConfig) validate() error {
	maximumRender := preview.DefaultLimits()
	maximumImage := preview.DefaultImageOutputLimits()
	checks := []struct {
		name  string
		value int
		limit int
	}{
		{name: "max_input_bytes", value: c.MaxInputBytes, limit: maximumRender.MaxInputBytes},
		{name: "max_json_bytes", value: c.MaxJSONBytes, limit: min(c.MaxInputBytes, maximumRender.MaxJSONBytes)},
		{name: "max_json_depth", value: c.MaxJSONDepth, limit: maximumRender.MaxJSONDepth},
		{name: "max_rendered_lines", value: c.MaxRenderedLines, limit: maximumRender.MaxRenderedLines},
		{name: "max_output_bytes", value: c.MaxOutputBytes, limit: maximumRender.MaxOutputBytes},
		{name: "max_style_spans", value: c.MaxStyleSpans, limit: maximumRender.MaxStyleSpans},
		{name: "image_max_output_bytes", value: c.ImageMaxOutputBytes, limit: maximumImage.MaxOutputBytes},
		{name: "image_max_payload_bytes", value: c.ImageMaxPayloadBytes, limit: min(c.ImageMaxOutputBytes, maximumImage.MaxPayloadBytes)},
		{name: "image_chunk_bytes", value: c.ImageChunkBytes, limit: min(c.ImageMaxPayloadBytes, maximumImage.ChunkBytes)},
	}
	for _, check := range checks {
		if check.value < 1 || check.value > check.limit {
			return fmt.Errorf("preview.%s must be within 1..%d", check.name, check.limit)
		}
	}
	if c.MaxImagePixels < 1 || c.MaxImagePixels > maximumRender.MaxImagePixels {
		return fmt.Errorf("preview.max_image_pixels must be within 1..%d", maximumRender.MaxImagePixels)
	}
	imagePixelLimit := min(c.MaxImagePixels, maximumImage.MaxPixels)
	if c.ImageMaxPixels < 1 || c.ImageMaxPixels > imagePixelLimit {
		return fmt.Errorf("preview.image_max_pixels must be within 1..%d", imagePixelLimit)
	}
	return nil
}

func (c SearchConfig) validate() error {
	maximumFilename := search.DefaultFilenameBudget()
	filenameChecks := []struct {
		name         string
		value, limit uint64
	}{
		{name: "page_items", value: uint64(c.Filename.PageItems), limit: uint64(maximumFilename.PageItems)},
		{name: "event_buffer", value: uint64(c.Filename.EventBuffer), limit: uint64(maximumFilename.EventBuffer)},
		{name: "concurrent_lists", value: uint64(c.Filename.ConcurrentLists), limit: uint64(maximumFilename.ConcurrentLists)},
		{name: "max_depth", value: uint64(c.Filename.MaxDepth), limit: uint64(maximumFilename.MaxDepth)},
		{name: "max_entries", value: c.Filename.MaxEntries, limit: maximumFilename.MaxEntries},
		{name: "max_results", value: c.Filename.MaxResults, limit: maximumFilename.MaxResults},
		{name: "max_output_bytes", value: c.Filename.MaxOutputBytes, limit: maximumFilename.MaxOutputBytes},
	}
	for _, check := range filenameChecks {
		if check.value < 1 || check.value > check.limit {
			return fmt.Errorf("search.filename.%s must be within 1..%d", check.name, check.limit)
		}
	}
	filenameDurationLimit := maximumFilename.MaxDuration.Milliseconds()
	if c.Filename.MaxDurationMS < 1 || c.Filename.MaxDurationMS > filenameDurationLimit {
		return fmt.Errorf("search.filename.max_duration_ms must be within 1..%d", filenameDurationLimit)
	}

	maximumContent := search.DefaultContentBudget()
	contentChecks := []struct {
		name         string
		value, limit uint64
	}{
		{name: "page_items", value: uint64(c.Content.PageItems), limit: uint64(maximumContent.PageItems)},
		{name: "event_buffer", value: uint64(c.Content.EventBuffer), limit: uint64(maximumContent.EventBuffer)},
		{name: "max_depth", value: uint64(c.Content.MaxDepth), limit: uint64(maximumContent.MaxDepth)},
		{name: "max_entries", value: c.Content.MaxEntries, limit: maximumContent.MaxEntries},
		{name: "max_files", value: c.Content.MaxFiles, limit: maximumContent.MaxFiles},
		{name: "max_results", value: c.Content.MaxResults, limit: maximumContent.MaxResults},
		{name: "max_matches_per_file", value: c.Content.MaxMatchesPerFile, limit: maximumContent.MaxMatchesPerFile},
		{name: "max_file_bytes", value: c.Content.MaxFileBytes, limit: min(c.Content.MaxReadBytes, maximumContent.MaxFileBytes)},
		{name: "max_read_bytes", value: c.Content.MaxReadBytes, limit: maximumContent.MaxReadBytes},
		{name: "max_snippet_bytes", value: uint64(c.Content.MaxSnippetBytes), limit: uint64(maximumContent.MaxSnippetBytes)},
		{name: "max_output_bytes", value: c.Content.MaxOutputBytes, limit: maximumContent.MaxOutputBytes},
	}
	for _, check := range contentChecks {
		if check.value < 1 || check.value > check.limit {
			return fmt.Errorf("search.content.%s must be within 1..%d", check.name, check.limit)
		}
	}
	contentDurationLimit := maximumContent.MaxDuration.Milliseconds()
	if c.Content.MaxDurationMS < 1 || c.Content.MaxDurationMS > contentDurationLimit {
		return fmt.Errorf("search.content.max_duration_ms must be within 1..%d", contentDurationLimit)
	}
	return nil
}

func (c RetryConfig) validate() error {
	defaultDelays := retrypolicy.DefaultReconnectDelays()
	if c.ReconnectDelaysMS == nil {
		return errors.New("retry.reconnect_delays_ms must be an array; use [] to disable automatic reconnects")
	}
	if len(c.ReconnectDelaysMS) > len(defaultDelays) {
		return fmt.Errorf("retry.reconnect_delays_ms must contain at most %d delays", len(defaultDelays))
	}
	var previous int64
	for index, value := range c.ReconnectDelaysMS {
		minimum := defaultDelays[index].Milliseconds()
		maximum := retrypolicy.MaxReconnectDelay.Milliseconds()
		if value < minimum || value > maximum {
			return fmt.Errorf("retry.reconnect_delays_ms[%d] must be within %d..%d", index, minimum, maximum)
		}
		if index > 0 && value < previous {
			return fmt.Errorf("retry.reconnect_delays_ms[%d] must not be less than the previous delay", index)
		}
		previous = value
	}
	minimumJobDelay := retrypolicy.DefaultJobDelay.Milliseconds()
	maximumJobDelay := retrypolicy.MaxJobDelay.Milliseconds()
	if c.JobRetryDelayMS < minimumJobDelay || c.JobRetryDelayMS > maximumJobDelay {
		return fmt.Errorf("retry.job_retry_delay_ms must be within %d..%d", minimumJobDelay, maximumJobDelay)
	}
	return nil
}

func (config ExternalConfig) validate() error {
	if config.Editor != nil {
		if err := config.Editor.validate(); err != nil {
			return fmt.Errorf("editor: %w", err)
		}
	}
	if config.Opener != nil {
		if err := config.Opener.validate(); err != nil {
			return fmt.Errorf("opener: %w", err)
		}
	}
	if len(config.Previewers) > maxExternalCommands {
		return fmt.Errorf("previewer count exceeds %d", maxExternalCommands)
	}
	names := make(map[string]struct{}, len(config.Previewers))
	for index, previewer := range config.Previewers {
		if previewer.Name == "" || len(previewer.Name) > maxExternalRuleNameBytes || !utf8.ValidString(previewer.Name) || hasASCIIControl(previewer.Name) {
			return fmt.Errorf("previewer %d name is invalid", index)
		}
		if _, duplicate := names[previewer.Name]; duplicate {
			return fmt.Errorf("previewer %d name %q is duplicated", index, previewer.Name)
		}
		names[previewer.Name] = struct{}{}
		if len(previewer.MediaTypes) == 0 && len(previewer.Extensions) == 0 {
			return fmt.Errorf("previewer %d match is empty", index)
		}
		if len(previewer.MediaTypes)+len(previewer.Extensions) > maxExternalMatchItems {
			return fmt.Errorf("previewer %d match item count exceeds %d", index, maxExternalMatchItems)
		}
		items := append(append([]string(nil), previewer.MediaTypes...), previewer.Extensions...)
		for _, item := range items {
			if item == "" || len(item) > maxExternalItemBytes || !utf8.ValidString(item) || hasASCIIControl(item) {
				return fmt.Errorf("previewer %d match item is invalid", index)
			}
		}
		for _, extension := range previewer.Extensions {
			if !strings.HasPrefix(extension, ".") || strings.ContainsAny(extension, `/\\`) {
				return fmt.Errorf("previewer %d extension %q is invalid", index, extension)
			}
		}
		if err := previewer.Command.validate(); err != nil {
			return fmt.Errorf("previewer %d command: %w", index, err)
		}
		if previewer.TimeoutMS <= 0 || previewer.TimeoutMS > maxExternalTimeoutMS {
			return fmt.Errorf("previewer %d timeout_ms must be in [1,%d]", index, maxExternalTimeoutMS)
		}
		if previewer.MaxInputBytes <= 0 || previewer.MaxInputBytes > maxExternalInputBytes {
			return fmt.Errorf("previewer %d max_input_bytes must be in [1,%d]", index, maxExternalInputBytes)
		}
	}
	return nil
}

func (command CommandConfig) validate() error {
	if command.Executable == "" || len(command.Args) > maxExternalArguments {
		return errors.New("executable is empty or argv is too large")
	}
	total := 0
	items := append([]string{command.Executable}, command.Args...)
	for _, item := range items {
		if len(item) > maxExternalItemBytes || !utf8.ValidString(item) || hasASCIIControl(item) {
			return errors.New("command item is invalid or too large")
		}
		total += len(item)
	}
	if total > maxExternalCommandBytes {
		return fmt.Errorf("executable and argv exceed %d bytes", maxExternalCommandBytes)
	}
	return nil
}

func hasASCIIControl(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return true
		}
	}
	return false
}
