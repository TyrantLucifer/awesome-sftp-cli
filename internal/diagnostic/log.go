package diagnostic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/platform"
)

const (
	persistentMessage = "diagnostic"
	defaultMaxBytes   = 4 * 1024 * 1024
	defaultBackups    = 3
)

type Config struct {
	MaxBytes     int64
	Backups      int
	RingCapacity int
	Level        *slog.LevelVar
}

func DefaultConfig() Config {
	return Config{MaxBytes: defaultMaxBytes, Backups: defaultBackups, RingCapacity: defaultRingCapacity}
}

type DaemonLog struct {
	Logger  *slog.Logger
	Level   *slog.LevelVar
	Records *Ring

	writer *rollingWriter
}

func OpenDaemon(path string, config Config) (*DaemonLog, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("open daemon log: path must be canonical and absolute")
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = defaultMaxBytes
	}
	if config.MaxBytes < 256 {
		return nil, errors.New("open daemon log: maximum size must be at least 256 bytes")
	}
	if config.Backups == 0 {
		config.Backups = defaultBackups
	}
	if config.Backups < 0 || config.Backups > 16 {
		return nil, errors.New("open daemon log: backup count must be between 0 and 16")
	}
	if err := platform.PreparePrivateDirectory(filepath.Dir(path), platform.ValidatePersistent); err != nil {
		return nil, fmt.Errorf("open daemon log: prepare directory: %w", err)
	}
	writer, err := openRollingWriter(path, config.MaxBytes, config.Backups)
	if err != nil {
		return nil, err
	}
	level := config.Level
	if level == nil {
		level = &slog.LevelVar{}
	}
	records := NewRing(config.RingCapacity)
	return &DaemonLog{
		Logger:  slog.New(newFanoutHandler(NewJSONHandler(writer, level), NewRingHandler(records, level))),
		Level:   level,
		Records: records,
		writer:  writer,
	}, nil
}

func (log *DaemonLog) Close() error {
	if log == nil || log.writer == nil {
		return nil
	}
	return log.writer.Close()
}

func NewJSONHandler(destination io.Writer, level slog.Leveler) slog.Handler {
	options := &slog.HandlerOptions{Level: level}
	return newAllowlistHandler(slog.NewJSONHandler(destination, options), allowPersistentAttr, true)
}

func Component(value string) slog.Attr { return slog.String("component", value) }
func Event(value string) slog.Attr     { return slog.String("event", value) }
func EndpointID(value domain.EndpointID) slog.Attr {
	return slog.String("endpoint_id", string(value))
}
func JobID(value domain.JobID) slog.Attr { return slog.String("job_id", string(value)) }
func RequestID(value domain.RequestID) slog.Attr {
	return slog.String("request_id", string(value))
}
func ErrorCode(value domain.Code) slog.Attr { return slog.String("error_code", string(value)) }

type attrFilter func(groups []string, attr slog.Attr) bool

type allowlistHandler struct {
	next            slog.Handler
	allow           attrFilter
	sanitizeMessage bool
	groups          []string
}

func newAllowlistHandler(next slog.Handler, allow attrFilter, sanitizeMessage bool) slog.Handler {
	return &allowlistHandler{next: next, allow: allow, sanitizeMessage: sanitizeMessage}
}

func (handler *allowlistHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *allowlistHandler) Handle(ctx context.Context, record slog.Record) error {
	message := record.Message
	if handler.sanitizeMessage {
		message = persistentMessage
	}
	safe := slog.NewRecord(record.Time, record.Level, message, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		if handler.allow(handler.groups, attr) {
			safe.AddAttrs(attr)
		}
		return true
	})
	return handler.next.Handle(ctx, safe)
}

func (handler *allowlistHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	safe := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if handler.allow(handler.groups, attr) {
			safe = append(safe, attr)
		}
	}
	return &allowlistHandler{
		next:            handler.next.WithAttrs(safe),
		allow:           handler.allow,
		sanitizeMessage: handler.sanitizeMessage,
		groups:          append([]string(nil), handler.groups...),
	}
}

func (handler *allowlistHandler) WithGroup(name string) slog.Handler {
	groups := append(append([]string(nil), handler.groups...), name)
	return &allowlistHandler{
		next:            handler.next.WithGroup(name),
		allow:           handler.allow,
		sanitizeMessage: handler.sanitizeMessage,
		groups:          groups,
	}
}

func allowPersistentAttr(groups []string, attr slog.Attr) bool {
	if len(groups) != 0 || attr.Value.Kind() != slog.KindString {
		return false
	}
	value := attr.Value.String()
	switch attr.Key {
	case "component":
		return IsReviewedComponent(value)
	case "event":
		return IsReviewedEvent(value)
	case "endpoint_id":
		_, err := domain.ParseEndpointID(value)
		return err == nil
	case "job_id":
		_, err := domain.ParseJobID(value)
		return err == nil
	case "request_id":
		_, err := domain.ParseRequestID(value)
		return err == nil
	case "error_code":
		return IsReviewedErrorCode(domain.Code(value))
	default:
		return false
	}
}

var reviewedComponents = map[string]struct{}{
	"cache": {}, "daemon": {}, "helper": {}, "state": {}, "transfer": {},
}

var reviewedEvents = map[string]struct{}{
	"cache_initialized":            {},
	"cache_unavailable":            {},
	"connection_failed":            {},
	"helper_lifecycle_unavailable": {},
	"progress":                     {},
	"read_only_degraded":           {},
	"rpc_request_failed":           {},
	"rpc_request_started":          {},
	"rpc_request_succeeded":        {},
}

func IsReviewedComponent(value string) bool {
	_, ok := reviewedComponents[value]
	return ok
}

func IsReviewedEvent(value string) bool {
	_, ok := reviewedEvents[value]
	return ok
}

func IsReviewedErrorCode(code domain.Code) bool {
	switch code {
	case domain.CodeInvalidArgument,
		domain.CodeNotFound,
		domain.CodeAlreadyExists,
		domain.CodePermissionDenied,
		domain.CodeAuthRequired,
		domain.CodeTransportInterrupted,
		domain.CodeTimeout,
		domain.CodeUnsupported,
		domain.CodeCapabilityLost,
		domain.CodeConflict,
		domain.CodeResourceExhausted,
		domain.CodeIntegrityFailed,
		domain.CodeCanceled,
		domain.CodeProtocolIncompatible,
		domain.CodeInternal:
		return true
	default:
		return false
	}
}

type rollingWriter struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	maxBytes int64
	backups  int
	closed   bool
}

func openRollingWriter(path string, maxBytes int64, backups int) (*rollingWriter, error) {
	for index := 0; index <= backups; index++ {
		candidate := path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", path, index)
		}
		if err := validateExistingPrivateFile(candidate); err != nil {
			return nil, fmt.Errorf("open daemon log: validate existing file: %w", err)
		}
	}
	file, size, err := openPrivateLogFile(path)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}
	return &rollingWriter{path: path, file: file, size: size, maxBytes: maxBytes, backups: backups}, nil
}

func (writer *rollingWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, os.ErrClosed
	}
	if int64(len(data)) > writer.maxBytes {
		return 0, errors.New("write daemon log: record exceeds maximum file size")
	}
	if writer.size > 0 && writer.size+int64(len(data)) > writer.maxBytes {
		if err := writer.rotate(); err != nil {
			return 0, err
		}
	}
	written, err := writer.file.Write(data)
	writer.size += int64(written)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	return written, err
}

func (writer *rollingWriter) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	if err := writer.file.Sync(); err != nil {
		_ = writer.file.Close()
		return fmt.Errorf("close daemon log: sync: %w", err)
	}
	if err := writer.file.Close(); err != nil {
		return fmt.Errorf("close daemon log: %w", err)
	}
	return nil
}

func (writer *rollingWriter) rotate() error {
	for index := 0; index <= writer.backups; index++ {
		candidate := writer.path
		if index > 0 {
			candidate = fmt.Sprintf("%s.%d", writer.path, index)
		}
		if err := validateExistingPrivateFile(candidate); err != nil {
			return fmt.Errorf("rotate daemon log: %w", err)
		}
	}
	if err := writer.file.Sync(); err != nil {
		return fmt.Errorf("rotate daemon log: sync: %w", err)
	}
	if err := writer.file.Close(); err != nil {
		return fmt.Errorf("rotate daemon log: close: %w", err)
	}
	reopen := func(cause error) error {
		file, size, openErr := openPrivateLogFile(writer.path)
		if openErr == nil {
			writer.file = file
			writer.size = size
			return cause
		}
		return errors.Join(cause, fmt.Errorf("reopen daemon log: %w", openErr))
	}
	if writer.backups > 0 {
		oldest := fmt.Sprintf("%s.%d", writer.path, writer.backups)
		if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return reopen(fmt.Errorf("rotate daemon log: remove oldest backup: %w", err))
		}
		for index := writer.backups - 1; index >= 1; index-- {
			from := fmt.Sprintf("%s.%d", writer.path, index)
			to := fmt.Sprintf("%s.%d", writer.path, index+1)
			if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
				return reopen(fmt.Errorf("rotate daemon log: rename backup: %w", err))
			}
		}
		if err := os.Rename(writer.path, writer.path+".1"); err != nil {
			return reopen(fmt.Errorf("rotate daemon log: archive current file: %w", err))
		}
	} else if err := os.Remove(writer.path); err != nil {
		return reopen(fmt.Errorf("rotate daemon log: remove current file: %w", err))
	}
	file, size, err := openPrivateLogFile(writer.path)
	if err != nil {
		return fmt.Errorf("rotate daemon log: create current file: %w", err)
	}
	writer.file = file
	writer.size = size
	return nil
}

func validateExistingPrivateFile(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return platform.ValidatePrivateFile(path, platform.ValidatePersistent)
}

func openPrivateLogFile(path string) (*os.File, int64, error) {
	// #nosec G304 -- path is a canonical application-owned log path whose complete trust chain is validated above.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, 0, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	if err := platform.ValidatePrivateFile(path, platform.ValidatePersistent); err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

var _ io.WriteCloser = (*rollingWriter)(nil)
var _ slog.Handler = (*allowlistHandler)(nil)
