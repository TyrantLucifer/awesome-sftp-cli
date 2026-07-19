package diagnostic

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

const (
	defaultRingCapacity = 1000
	maxQueryRecords     = 256
)

type Record struct {
	Sequence   uint64            `json:"sequence"`
	Time       time.Time         `json:"time"`
	Level      string            `json:"level"`
	Message    string            `json:"message"`
	Component  string            `json:"component,omitempty"`
	Event      string            `json:"event,omitempty"`
	EndpointID domain.EndpointID `json:"endpoint_id,omitempty"`
	JobID      domain.JobID      `json:"job_id,omitempty"`
	RequestID  domain.RequestID  `json:"request_id,omitempty"`
	ErrorCode  domain.Code       `json:"error_code,omitempty"`
}

type Query struct {
	AfterSequence uint64            `json:"after_sequence,omitempty"`
	Limit         int               `json:"limit,omitempty"`
	EndpointID    domain.EndpointID `json:"endpoint_id,omitempty"`
	JobID         domain.JobID      `json:"job_id,omitempty"`
}

type Page struct {
	Records []Record `json:"records"`
	More    bool     `json:"more"`
}

type Ring struct {
	mu       sync.Mutex
	records  []Record
	capacity int
	next     uint64
}

func NewRing(capacity int) *Ring {
	if capacity <= 0 || capacity > defaultRingCapacity {
		capacity = defaultRingCapacity
	}
	return &Ring{records: make([]Record, 0, capacity), capacity: capacity}
}

func (ring *Ring) Query(query Query) Page {
	if ring == nil {
		return Page{Records: []Record{}}
	}
	limit := query.Limit
	if limit <= 0 || limit > maxQueryRecords {
		limit = maxQueryRecords
	}
	ring.mu.Lock()
	defer ring.mu.Unlock()
	page := Page{Records: make([]Record, 0, min(limit, len(ring.records)))}
	for _, record := range ring.records {
		if record.Sequence <= query.AfterSequence || query.JobID != "" && record.JobID != query.JobID || query.EndpointID != "" && record.EndpointID != query.EndpointID {
			continue
		}
		if len(page.Records) == limit {
			page.More = true
			break
		}
		page.Records = append(page.Records, record)
	}
	return page
}

func NewRingHandler(ring *Ring, level slog.Leveler) slog.Handler {
	if ring == nil {
		ring = NewRing(0)
	}
	return newAllowlistHandler(&ringHandler{ring: ring, level: level}, allowPersistentAttr, true)
}

type ringHandler struct {
	ring  *Ring
	level slog.Leveler
	attrs []slog.Attr
}

func (handler *ringHandler) Enabled(_ context.Context, level slog.Level) bool {
	return handler.level == nil || level >= handler.level.Level()
}

func (handler *ringHandler) Handle(_ context.Context, source slog.Record) error {
	record := Record{Time: source.Time.UTC(), Level: source.Level.String(), Message: source.Message}
	for _, attr := range handler.attrs {
		setRecordAttr(&record, attr)
	}
	source.Attrs(func(attr slog.Attr) bool {
		setRecordAttr(&record, attr)
		return true
	})
	handler.ring.mu.Lock()
	handler.ring.next++
	record.Sequence = handler.ring.next
	if len(handler.ring.records) == handler.ring.capacity {
		copy(handler.ring.records, handler.ring.records[1:])
		handler.ring.records[len(handler.ring.records)-1] = record
	} else {
		handler.ring.records = append(handler.ring.records, record)
	}
	handler.ring.mu.Unlock()
	return nil
}

func (handler *ringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := make([]slog.Attr, 0, len(handler.attrs)+len(attrs))
	combined = append(combined, handler.attrs...)
	combined = append(combined, attrs...)
	return &ringHandler{ring: handler.ring, level: handler.level, attrs: combined}
}

func (handler *ringHandler) WithGroup(string) slog.Handler { return handler }

func setRecordAttr(record *Record, attr slog.Attr) {
	value := attr.Value.String()
	switch attr.Key {
	case "component":
		record.Component = value
	case "event":
		record.Event = value
	case "endpoint_id":
		record.EndpointID = domain.EndpointID(value)
	case "job_id":
		record.JobID = domain.JobID(value)
	case "request_id":
		record.RequestID = domain.RequestID(value)
	case "error_code":
		record.ErrorCode = domain.Code(value)
	}
}

type fanoutHandler struct {
	handlers []slog.Handler
}

func newFanoutHandler(handlers ...slog.Handler) slog.Handler {
	return &fanoutHandler{handlers: append([]slog.Handler(nil), handlers...)}
}

func (handler *fanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, next := range handler.handlers {
		if next.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (handler *fanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var result error
	for _, next := range handler.handlers {
		if next.Enabled(ctx, record.Level) {
			result = errors.Join(result, next.Handle(ctx, record.Clone()))
		}
	}
	return result
}

func (handler *fanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(handler.handlers))
	for index, child := range handler.handlers {
		next[index] = child.WithAttrs(attrs)
	}
	return &fanoutHandler{handlers: next}
}

func (handler *fanoutHandler) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(handler.handlers))
	for index, child := range handler.handlers {
		next[index] = child.WithGroup(name)
	}
	return &fanoutHandler{handlers: next}
}
