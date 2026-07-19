package cachestore

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
)

func TestUploadReferencesCannotAttachToClaimedJobHistory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	database := newDatabaseAtHead(t, ctx, 4)
	store := newStore(t, ctx, database)
	blob, entry, materialization, _, _ := validGraph()
	if err := store.Publish(ctx, blob, entry); err != nil {
		t.Fatal(err)
	}
	jobID := "job_" + strings.Repeat("a", 26)
	seedClaimedHistoryJob(t, ctx, database, jobID)
	upload := cache.Reference{
		ID: cache.ReferenceID(strings.Repeat("1", 32)), OwnerKind: cache.ReferenceOwnerUpload,
		OwnerID: jobID, Target: cache.BlobTarget(blob.ID), CreatedAt: time.Unix(20, 0).UTC(),
	}
	if err := store.AddReference(ctx, upload); err == nil || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("AddReference for claimed Job error = %v", err)
	}

	preview := upload
	preview.ID = cache.ReferenceID(strings.Repeat("2", 32))
	preview.OwnerKind = cache.ReferenceOwnerPreview
	if err := store.AddReference(ctx, preview); err != nil {
		t.Fatalf("same owner ID under non-upload kind: %v", err)
	}

	reference := cache.Reference{
		ID: cache.ReferenceID(strings.Repeat("3", 32)), OwnerKind: cache.ReferenceOwnerUpload,
		OwnerID: jobID, Target: cache.MaterializationTarget(materialization.ID), CreatedAt: time.Unix(20, 0).UTC(),
	}
	lease := cache.Lease{
		ID: cache.LeaseID(strings.Repeat("4", 32)), OwnerKind: cache.LeaseOwnerUpload, OwnerID: jobID,
		DaemonInstanceID: strings.Repeat("5", 32), Target: cache.MaterializationTarget(materialization.ID), State: cache.LeaseActive,
		HeartbeatAt: time.Unix(20, 0).UTC(), ExpiresAt: time.Unix(30, 0).UTC(), GraceUntil: time.Unix(40, 0).UTC(),
	}
	if err := store.PrepareHandoff(ctx, materialization, reference, lease); err == nil || !strings.Contains(err.Error(), "retention") {
		t.Fatalf("PrepareHandoff for claimed Job error = %v", err)
	}

	if _, err := database.ExecContext(ctx, "DELETE FROM job_history_retention WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	upload.ID = cache.ReferenceID(strings.Repeat("6", 32))
	if err := store.AddReference(ctx, upload); err != nil {
		t.Fatalf("AddReference after claim release: %v", err)
	}
}

func seedClaimedHistoryJob(t *testing.T, ctx context.Context, database *sql.DB, jobID string) {
	t.Helper()
	planID := "plan_" + strings.TrimPrefix(jobID, "job_")
	if _, err := database.ExecContext(ctx, "INSERT INTO operation_plans(plan_id,request_id,kind,source_json,destination_json,route,verification,conflict_policy,risk_class,frozen_at_unix) VALUES(?,?,'copy','{}','{}','local_relay','size','ask','ordinary',1)", planID, "req_"+strings.Repeat("b", 26)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO jobs(job_id,plan_id,state,state_version,next_event_sequence,pause_requested,cancel_requested,created_at_unix,updated_at_unix,terminal_summary) VALUES(?,?,'completed',1,1,0,0,1,1,'done')", jobID, planID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO job_history_retention(singleton,job_id,policy_version,claimed_at_unix) VALUES(1,?,1,2)", jobID); err != nil {
		t.Fatal(err)
	}
}
