package diagnostic

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/testkit"
)

func TestRingRetainsOnlyBoundedSanitizedRecords(t *testing.T) {
	ring := NewRing(3)
	logger := slog.New(NewRingHandler(ring, nil))
	for index := 0; index < 5; index++ {
		logger.InfoContext(context.Background(), "secret message "+fmt.Sprint(index),
			Component("cache"), Event("cache_initialized"),
			slog.String("path", "/private/secret"),
		)
	}

	page := ring.Query(Query{})
	if len(page.Records) != 3 || page.Records[0].Sequence != 3 || page.Records[2].Sequence != 5 {
		t.Fatalf("records = %#v, want sequences 3..5", page.Records)
	}
	for _, record := range page.Records {
		if record.Message != persistentMessage || record.Component != "cache" || record.Event != "cache_initialized" {
			t.Fatalf("unsafe or incomplete record = %#v", record)
		}
	}
}

func TestOpenDaemonFansOutToBoundedRing(t *testing.T) {
	path := filepath.Join(testkit.PersistentTempDir(t), "logs", "daemon.jsonl")
	log, err := OpenDaemon(path, Config{RingCapacity: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	for index := 0; index < 3; index++ {
		log.Logger.Error("secret", Component("daemon"), Event("connection_failed"), slog.Int("index", index), slog.String("path", "/secret"))
	}
	page := log.Records.Query(Query{})
	if len(page.Records) != 2 || page.Records[0].Sequence != 2 || page.Records[0].Message != persistentMessage || page.Records[0].Event != "connection_failed" {
		t.Fatalf("records = %#v", page.Records)
	}
}

func TestRingQueryCapsPagesAndFiltersCorrelation(t *testing.T) {
	ring := NewRing(1000)
	logger := slog.New(NewRingHandler(ring, nil))
	jobA := domain.JobID("job_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	jobB := domain.JobID("job_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	endpointA := domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	endpointB := domain.EndpointID("ep_bbbbbbbbbbbbbbbbbbbbbbbbbb")
	for index := 0; index < 300; index++ {
		jobID, endpointID := jobA, endpointA
		if index%2 != 0 {
			jobID, endpointID = jobB, endpointB
		}
		logger.Info("unsafe", Component("transfer"), Event("progress"), JobID(jobID), EndpointID(endpointID))
	}

	page := ring.Query(Query{AfterSequence: 10, Limit: 999})
	if len(page.Records) != 256 || !page.More || page.Records[0].Sequence != 11 {
		t.Fatalf("page = %#v, want capped continuation", page)
	}
	filtered := ring.Query(Query{JobID: jobB, EndpointID: endpointB, Limit: 20})
	if len(filtered.Records) != 20 || !filtered.More {
		t.Fatalf("filtered page = %#v", filtered)
	}
	for _, record := range filtered.Records {
		if record.JobID != jobB || record.EndpointID != endpointB {
			t.Fatalf("filter leaked record = %#v", record)
		}
	}
}

func TestRingHandlerHonorsLevelAndRecordTime(t *testing.T) {
	ring := NewRing(10)
	level := &slog.LevelVar{}
	level.Set(slog.LevelWarn)
	logger := slog.New(NewRingHandler(ring, level))
	logger.Info("hidden", Component("daemon"), Event("rpc_request_started"))
	wantTime := time.Unix(123, 456).UTC()
	record := slog.NewRecord(wantTime, slog.LevelError, "unsafe", 0)
	record.AddAttrs(Component("daemon"), Event("rpc_request_failed"), ErrorCode(domain.CodeInternal))
	if err := logger.Handler().Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}

	page := ring.Query(Query{})
	if len(page.Records) != 1 || !page.Records[0].Time.Equal(wantTime) || page.Records[0].Level != slog.LevelError.String() || page.Records[0].ErrorCode != domain.CodeInternal {
		t.Fatalf("records = %#v", page.Records)
	}
}
