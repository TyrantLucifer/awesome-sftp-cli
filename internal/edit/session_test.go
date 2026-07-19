package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/cache"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

func TestObservePostEditorChangeMatrixFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		local      LocalObservation
		remote     RemoteObservation
		wantState  State
		wantAction Action
	}{
		{"local and remote unchanged", localPresent(testDigest("b")), remotePresent(testRemoteFingerprint("remote-v1")), StateNoChanges, ActionNoUpload},
		{"local content changed remote unchanged", localPresent(testDigest("c")), remotePresent(testRemoteFingerprint("remote-v1")), StateAwaitingUploadConfirmation, ActionConfirmUpload},
		{"remote modified", localPresent(testDigest("b")), remotePresent(testRemoteFingerprint("remote-v2")), StateRemoteChanged, ActionRefreshRemote},
		{"remote replaced by symlink", localPresent(testDigest("b")), RemoteObservation{Status: RemotePresent, Kind: domain.EntrySymlink}, StateRemoteChanged, ActionRefreshRemote},
		{"remote deleted", localPresent(testDigest("b")), RemoteObservation{Status: RemoteDeleted}, StateRemoteChanged, ActionRefreshRemote},
		{"both changed", localPresent(testDigest("c")), remotePresent(testRemoteFingerprint("remote-v2")), StateConflict, ActionResolveConflict},
		{"local changed remote deleted", localPresent(testDigest("c")), RemoteObservation{Status: RemoteDeleted}, StateConflict, ActionResolveConflict},
		{"local deleted", LocalObservation{Status: LocalDeleted}, remotePresent(testRemoteFingerprint("remote-v1")), StateRecoveryRequired, ActionRecoverLocal},
		{"local hash uncertain", LocalObservation{Status: LocalPresent}, remotePresent(testRemoteFingerprint("remote-v1")), StateRecoveryRequired, ActionRecoverLocal},
		{"local unavailable", LocalObservation{Status: LocalUnavailable}, remotePresent(testRemoteFingerprint("remote-v1")), StateRecoveryRequired, ActionRecoverLocal},
		{"remote unstat unavailable", localPresent(testDigest("b")), RemoteObservation{Status: RemoteUnavailable}, StateRecoveryRequired, ActionRecoverLocal},
		{"remote fingerprint uncertain", localPresent(testDigest("b")), RemoteObservation{Status: RemotePresent, Kind: domain.EntryFile}, StateRecoveryRequired, ActionRecoverLocal},
		{"remote unknown", localPresent(testDigest("b")), RemoteObservation{Status: RemoteUnknown}, StateRecoveryRequired, ActionRecoverLocal},
		{"local unknown", LocalObservation{Status: LocalUnknown}, remotePresent(testRemoteFingerprint("remote-v1")), StateRecoveryRequired, ActionRecoverLocal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := startTestSession(t, PurposeEditor)
			observed, result, err := session.Observe(session.Version(), PostEditorObservation{Local: test.local, Remote: test.remote})
			if err != nil {
				t.Fatalf("Observe() error = %v", err)
			}
			if observed.State() != test.wantState {
				t.Fatalf("State() = %q, want %q", observed.State(), test.wantState)
			}
			if result.Action != test.wantAction {
				t.Fatalf("Action = %q, want %q", result.Action, test.wantAction)
			}
		})
	}
}

func TestObserveDetectsSameFingerprintContentRewriteAndUncertainContentIdentity(t *testing.T) {
	baselineFingerprint := testRemoteFingerprint("remote-v1")
	baselineContent := testContentDigest("remote-content-a")
	request := testNewSessionRequest(PurposeEditor)
	request.Baseline.ExpectedRemote.Fingerprint = baselineFingerprint
	request.Baseline.ExpectedRemote.ContentSHA256 = baselineContent
	session, err := NewSession(request)
	if err != nil {
		t.Fatal(err)
	}
	session, err = session.BeginObservation(session.Version())
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name        string
		content     SHA256
		wantState   State
		wantChanged bool
	}{
		{name: "same content", content: baselineContent, wantState: StateAwaitingUploadConfirmation},
		{name: "same metadata rewritten content", content: testContentDigest("remote-content-b"), wantState: StateConflict, wantChanged: true},
		{name: "content identity unavailable", wantState: StateRecoveryRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			observed, evaluation, observeErr := session.Observe(session.Version(), PostEditorObservation{
				Local:  localPresent(testDigest("c")),
				Remote: RemoteObservation{Status: RemotePresent, Kind: domain.EntryFile, Fingerprint: baselineFingerprint, ContentSHA256: test.content},
			})
			if observeErr != nil {
				t.Fatal(observeErr)
			}
			if observed.State() != test.wantState || evaluation.RemoteChanged != test.wantChanged {
				t.Fatalf("observation = state %q evaluation %#v", observed.State(), evaluation)
			}
		})
	}
}

func TestOpenerObservationNeverCreatesSyncBack(t *testing.T) {
	session := startTestSession(t, PurposeOpener)
	observed, result, err := session.Observe(session.Version(), PostEditorObservation{
		Local:  localPresent(testDigest("c")),
		Remote: remotePresent(testRemoteFingerprint("remote-v1")),
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if result.Action != ActionConfirmUpload || observed.State() != StateAwaitingUploadConfirmation {
		t.Fatalf("observation = (%q, %q), want explicit confirmation", observed.State(), result.Action)
	}
	if observed.HasFrozenSyncBack() {
		t.Fatal("opener observation froze a sync-back without an explicit decision")
	}
}

func TestNoChangeAndRemoteOnlyCannotFreezeUpload(t *testing.T) {
	tests := []struct {
		name   string
		local  LocalObservation
		remote RemoteObservation
	}{
		{"no change", localPresent(testDigest("b")), remotePresent(testRemoteFingerprint("remote-v1"))},
		{"remote only", localPresent(testDigest("b")), remotePresent(testRemoteFingerprint("remote-v2"))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := startTestSession(t, PurposeEditor)
			observed, _, err := session.Observe(session.Version(), PostEditorObservation{Local: test.local, Remote: test.remote})
			if err != nil {
				t.Fatalf("Observe() error = %v", err)
			}
			if observed.HasFrozenSyncBack() {
				t.Fatal("observation froze sync-back")
			}
			if _, _, err := observed.Decide(observed.Version(), Decision{Kind: DecisionUpload}); err == nil {
				t.Fatal("upload decision succeeded without a local-only confirmation state")
			}
		})
	}
}

func TestSessionRejectsInvalidAndStaleTransitions(t *testing.T) {
	created := newTestSession(t, PurposeEditor)
	if _, _, err := created.Observe(created.Version(), PostEditorObservation{}); err == nil {
		t.Fatal("Observe() before BeginObservation succeeded")
	}
	started, err := created.BeginObservation(created.Version())
	if err != nil {
		t.Fatalf("BeginObservation() error = %v", err)
	}
	if _, err := started.BeginObservation(started.Version()); err == nil {
		t.Fatal("second BeginObservation() succeeded")
	}
	if _, _, err := started.Observe(created.Version(), PostEditorObservation{}); err == nil {
		t.Fatal("Observe() with stale optimistic version succeeded")
	}
	observed, _, err := started.Observe(started.Version(), PostEditorObservation{
		Local:  localPresent(testDigest("b")),
		Remote: remotePresent(testRemoteFingerprint("remote-v1")),
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if _, _, err := observed.Observe(observed.Version(), PostEditorObservation{}); err == nil {
		t.Fatal("second Observe() succeeded")
	}
}

func TestNewSessionValidatesTypedIdentityAndBaseline(t *testing.T) {
	request := testNewSessionRequest(PurposeEditor)
	request.ID = SessionID("UPPERCASE")
	if _, err := NewSession(request); err == nil {
		t.Fatal("NewSession() accepted invalid session ID")
	}

	request = testNewSessionRequest(PurposeEditor)
	request.Baseline.LocalSHA256 = SHA256("uncertain")
	if _, err := NewSession(request); err == nil {
		t.Fatal("NewSession() accepted uncertain local baseline hash")
	}

	request = testNewSessionRequest(PurposeEditor)
	request.Baseline.ExpectedRemote.Fingerprint = domain.Fingerprint{}
	if _, err := NewSession(request); err == nil {
		t.Fatal("NewSession() accepted weak remote baseline fingerprint")
	}

	request = testNewSessionRequest(PurposeEditor)
	request.Baseline.ExpectedRemote.ContentSHA256 = SHA256("uncertain")
	if _, err := NewSession(request); err == nil {
		t.Fatal("NewSession() accepted malformed remote content identity")
	}
}

func TestNewSessionOwnsBaselineFingerprint(t *testing.T) {
	request := testNewSessionRequest(PurposeEditor)
	version := "remote-v1"
	request.Baseline.ExpectedRemote.Fingerprint.VersionID = &version
	session, err := NewSession(request)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	version = "remote-v2"
	if got := fingerprintVersion(session.Baseline().ExpectedRemote.Fingerprint); got != "remote-v1" {
		t.Fatalf("caller mutation rewrote baseline fingerprint to %q", got)
	}
}

func TestRestorePersistentRejectsContradictoryMachineState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PersistentSession)
	}{
		{name: "ready version drift", mutate: func(value *PersistentSession) { value.Version = 2 }},
		{name: "frozen flag outside sync back", mutate: func(value *PersistentSession) { value.Frozen = true }},
		{name: "sync back without frozen flag", mutate: func(value *PersistentSession) {
			value.Version = 4
			value.State = StateSyncBackFrozen
			value.CurrentLocal = testDigest("c")
			value.Evaluation = Evaluation{Action: ActionConfirmUpload, LocalChanged: true}
		}},
		{name: "post observation action mismatch", mutate: func(value *PersistentSession) {
			value.Version = 3
			value.State = StateNoChanges
			value.CurrentLocal = value.Baseline.LocalSHA256
			value.Evaluation = Evaluation{Action: ActionResolveConflict, LocalChanged: true, RemoteChanged: true}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := newTestSession(t, PurposeEditor).Persistent()
			test.mutate(&value)
			if _, err := RestorePersistent(value); err == nil {
				t.Fatal("RestorePersistent accepted contradictory state")
			}
		})
	}
}

func TestExplicitDecisionsFreezeSyncBackPreconditions(t *testing.T) {
	t.Run("ordinary upload retains original baseline", func(t *testing.T) {
		session := observeLocalOnly(t)
		decided, request, err := session.Decide(session.Version(), Decision{Kind: DecisionUpload})
		if err != nil {
			t.Fatalf("Decide(upload) error = %v", err)
		}
		if decided.State() != StateSyncBackFrozen || request == nil {
			t.Fatalf("Decide(upload) = (%q, %#v)", decided.State(), request)
		}
		if request.PlanVersion != SyncBackPlanVersion || request.Origin != SyncBackOrigin {
			t.Fatalf("sync-back contract = version %d origin %q", request.PlanVersion, request.Origin)
		}
		if request.Decision != DecisionUpload || !equalPrecondition(request.OriginalDestinationPrecondition, request.DestinationPrecondition) {
			t.Fatal("ordinary upload did not preserve the original destination precondition")
		}
		if request.LocalSHA256 != testDigest("c") {
			t.Fatalf("LocalSHA256 = %q", request.LocalSHA256)
		}
	})

	t.Run("save as is expected absent", func(t *testing.T) {
		session := observeLocalOnly(t)
		target, _ := domain.NewLocation("remote", "/elsewhere.txt")
		_, request, err := session.Decide(session.Version(), Decision{Kind: DecisionSaveAs, SaveAsTarget: target})
		if err != nil {
			t.Fatalf("Decide(save-as) error = %v", err)
		}
		if request.Target != target || request.DestinationPrecondition.Presence != ExpectedAbsent {
			t.Fatalf("save-as request = %#v", request)
		}
	})

	t.Run("skip never freezes upload", func(t *testing.T) {
		session := observeLocalOnly(t)
		decided, request, err := session.Decide(session.Version(), Decision{Kind: DecisionSkip})
		if err != nil {
			t.Fatalf("Decide(skip) error = %v", err)
		}
		if decided.State() != StateSkipped || request != nil || decided.HasFrozenSyncBack() {
			t.Fatalf("skip result = (%q, %#v)", decided.State(), request)
		}
	})
}

func TestExplicitOverwriteRenewsButNeverReplacesOriginalPrecondition(t *testing.T) {
	session := startTestSession(t, PurposeEditor)
	observed, _, err := session.Observe(session.Version(), PostEditorObservation{
		Local:  localPresent(testDigest("c")),
		Remote: remotePresent(testRemoteFingerprint("remote-v2")),
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	newVersion := "remote-v2"
	renewalFingerprint := testRemoteFingerprint(newVersion)
	renewalFingerprint.VersionID = &newVersion
	renewalObservation := remotePresent(renewalFingerprint)
	decided, request, err := observed.Decide(observed.Version(), Decision{
		Kind:           DecisionOverwrite,
		ObservedRemote: renewalObservation,
		AuditReason:    "user confirmed overwrite after reviewing conflict",
	})
	if err != nil {
		t.Fatalf("Decide(overwrite) error = %v", err)
	}
	if request == nil || request.Renewal == nil {
		t.Fatal("overwrite omitted renewal audit")
	}
	if got := fingerprintVersion(request.OriginalDestinationPrecondition.Fingerprint); got != "remote-v1" {
		t.Fatalf("original fingerprint = %q, want remote-v1", got)
	}
	if got := fingerprintVersion(request.DestinationPrecondition.Fingerprint); got != "remote-v2" {
		t.Fatalf("renewed fingerprint = %q, want remote-v2", got)
	}
	if request.Renewal.Reason == "" || request.Renewal.FromVersion != observed.Version() || request.Renewal.ToVersion != decided.Version() {
		t.Fatalf("renewal audit = %#v", request.Renewal)
	}

	// The decision must own its observation. Mutating caller-owned pointers later
	// cannot rewrite either the original or renewed frozen precondition.
	newVersion = "remote-v3"
	if got := fingerprintVersion(request.DestinationPrecondition.Fingerprint); got != "remote-v2" {
		t.Fatalf("caller mutation rewrote frozen fingerprint to %q", got)
	}
	if got := fingerprintVersion(request.OriginalDestinationPrecondition.Fingerprint); got != "remote-v1" {
		t.Fatalf("caller mutation rewrote original fingerprint to %q", got)
	}
}

func TestExplicitOverwriteCanRenewADeletedDestinationAsExpectedAbsent(t *testing.T) {
	session := startTestSession(t, PurposeEditor)
	conflict, _, err := session.Observe(session.Version(), PostEditorObservation{
		Local:  localPresent(testDigest("c")),
		Remote: RemoteObservation{Status: RemoteDeleted},
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	_, request, err := conflict.Decide(conflict.Version(), Decision{
		Kind:           DecisionOverwrite,
		ObservedRemote: RemoteObservation{Status: RemoteDeleted},
		AuditReason:    "user confirmed recreation after remote deletion",
	})
	if err != nil {
		t.Fatalf("Decide(overwrite deleted) error = %v", err)
	}
	if request.DestinationPrecondition.Presence != ExpectedAbsent || request.Renewal == nil {
		t.Fatalf("deleted overwrite request = %#v", request)
	}
	if request.OriginalDestinationPrecondition.Presence != ExpectedPresent {
		t.Fatal("deleted overwrite discarded original expected-present baseline")
	}
}

func TestPersistentSessionRoundTripRestoresDecisionInputsAndOptimisticVersion(t *testing.T) {
	session := observeLocalOnly(t)
	persisted := session.Persistent()
	restored, err := RestorePersistent(persisted)
	if err != nil {
		t.Fatalf("RestorePersistent(): %v", err)
	}
	if restored.ID() != session.ID() || restored.Purpose() != session.Purpose() || restored.Version() != session.Version() || restored.State() != session.State() ||
		restored.CurrentLocalSHA256() != session.CurrentLocalSHA256() || !equalPrecondition(restored.Baseline().ExpectedRemote, session.Baseline().ExpectedRemote) {
		t.Fatalf("restored session = %#v, want %#v", restored.Persistent(), persisted)
	}
	if _, _, err := restored.Decide(restored.Version(), Decision{Kind: DecisionUpload}); err != nil {
		t.Fatalf("restored session cannot make original decision: %v", err)
	}
}

func TestOverwriteRejectsUnknownRenewalAndWrongState(t *testing.T) {
	localOnly := observeLocalOnly(t)
	if _, _, err := localOnly.Decide(localOnly.Version(), Decision{Kind: DecisionOverwrite, ObservedRemote: RemoteObservation{Status: RemoteUnknown}}); err == nil {
		t.Fatal("overwrite outside conflict state succeeded")
	}

	conflict := startTestSession(t, PurposeEditor)
	conflict, _, _ = conflict.Observe(conflict.Version(), PostEditorObservation{
		Local:  localPresent(testDigest("c")),
		Remote: remotePresent(testRemoteFingerprint("remote-v2")),
	})
	if _, _, err := conflict.Decide(conflict.Version(), Decision{Kind: DecisionOverwrite, ObservedRemote: RemoteObservation{Status: RemoteUnavailable}}); err == nil {
		t.Fatal("overwrite renewed from unavailable remote identity")
	}
}

func newTestSession(t *testing.T, purpose Purpose) Session {
	t.Helper()
	session, err := NewSession(testNewSessionRequest(purpose))
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	return session
}

func startTestSession(t *testing.T, purpose Purpose) Session {
	t.Helper()
	session := newTestSession(t, purpose)
	started, err := session.BeginObservation(session.Version())
	if err != nil {
		t.Fatalf("BeginObservation() error = %v", err)
	}
	return started
}

func observeLocalOnly(t *testing.T) Session {
	t.Helper()
	session := startTestSession(t, PurposeEditor)
	observed, _, err := session.Observe(session.Version(), PostEditorObservation{
		Local:  localPresent(testDigest("c")),
		Remote: remotePresent(testRemoteFingerprint("remote-v1")),
	})
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	return observed
}

func testNewSessionRequest(purpose Purpose) NewSessionRequest {
	target, _ := domain.NewLocation("remote", "/document.txt")
	return NewSessionRequest{
		ID:      SessionID(strings.Repeat("1", 32)),
		Purpose: purpose,
		Baseline: Baseline{
			SourceEntryID:     cache.EntryID(strings.Repeat("2", 64)),
			MaterializationID: cache.MaterializationID(strings.Repeat("3", 32)),
			Target:            target,
			ExpectedRemote: RemotePrecondition{
				Presence:      ExpectedPresent,
				Kind:          domain.EntryFile,
				Fingerprint:   testRemoteFingerprint("remote-v1"),
				ContentSHA256: testContentDigest("remote-v1"),
			},
			LocalSHA256: testDigest("b"),
		},
	}
}

func testDigest(character string) SHA256 { return SHA256(strings.Repeat(character, 64)) }

func testContentDigest(value string) SHA256 {
	digest := sha256.Sum256([]byte(value))
	return SHA256(hex.EncodeToString(digest[:]))
}

func testRemoteFingerprint(version string) domain.Fingerprint {
	size := uint64(12)
	modified := time.Unix(1_700_000_000, 0).UTC()
	precision := domain.TimePrecision("second")
	return domain.Fingerprint{Size: &size, ModifiedAt: &modified, ModifiedPrecision: &precision, VersionID: &version}
}

func localPresent(digest SHA256) LocalObservation {
	return LocalObservation{Status: LocalPresent, SHA256: digest}
}

func remotePresent(fingerprint domain.Fingerprint) RemoteObservation {
	content := "remote-content"
	if fingerprint.VersionID != nil {
		content = *fingerprint.VersionID
	}
	return RemoteObservation{Status: RemotePresent, Kind: domain.EntryFile, Fingerprint: fingerprint, ContentSHA256: testContentDigest(content)}
}

func fingerprintVersion(fingerprint domain.Fingerprint) string {
	if fingerprint.VersionID == nil {
		return ""
	}
	return *fingerprint.VersionID
}

func equalPrecondition(left, right RemotePrecondition) bool {
	return left.Presence == right.Presence && left.Kind == right.Kind && fingerprintVersion(left.Fingerprint) == fingerprintVersion(right.Fingerprint) && left.ContentSHA256 == right.ContentSHA256
}
