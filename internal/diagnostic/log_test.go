package diagnostic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/slogtest"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/testkit"
)

const (
	testRequestID  = domain.RequestID("req_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	testEndpointID = domain.EndpointID("ep_aaaaaaaaaaaaaaaaaaaaaaaaaa")
)

func TestAllowlistHandlerConformsToSlogContract(t *testing.T) {
	var output bytes.Buffer
	handler := newAllowlistHandler(
		slog.NewJSONHandler(&output, nil),
		func([]string, slog.Attr) bool { return true },
		false,
	)
	err := slogtest.TestHandler(handler, func() []map[string]any {
		var results []map[string]any
		scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
		for scanner.Scan() {
			var result map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
				t.Fatalf("decode slog record: %v", err)
			}
			results = append(results, result)
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		return results
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPersistentHandlerDropsSecretsAndUnregisteredFields(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, nil))
	secret := "secret-canary-password"
	logger.ErrorContext(context.Background(), secret,
		Component("daemon"),
		Event("rpc_request_failed"),
		RequestID(testRequestID),
		EndpointID(testEndpointID),
		ErrorCode(domain.CodeAuthRequired),
		slog.String("path", "/private/secret/path"),
		slog.Any("error", domain.FromContext("open", testEndpointID, nil, context.Canceled)),
		slog.String("request_id", "not-a-request-id"),
	)
	encoded := output.String()
	for _, forbidden := range []string{secret, "/private/secret/path", "operation canceled", "not-a-request-id", "\"path\"", "\"error\""} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("persistent log contains forbidden value %q: %s", forbidden, encoded)
		}
	}
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["msg"] != persistentMessage || record["request_id"] != string(testRequestID) || record["error_code"] != string(domain.CodeAuthRequired) {
		t.Fatalf("unexpected safe record: %#v", record)
	}
}

func TestPersistentHandlerDropsLexicallyValidButUnregisteredSecretValues(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(NewJSONHandler(&output, nil))
	secrets := []string{
		"username_canary_7f3a", "hostname_canary_7f3a", "private_key_canary_7f3a",
		"askpass_answer_canary_7f3a", "kerberos_ticket_canary_7f3a", "environment_secret_canary_7f3a",
		"command_argument_canary_7f3a", "file_content_canary_7f3a",
	}
	for _, secret := range secrets {
		logger.ErrorContext(context.Background(), secret,
			Component(secret),
			Event(secret),
			slog.String("error_code", secret),
			slog.String("path", "/private/"+secret),
		)
	}
	for _, secret := range secrets {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("persistent log retained safe-shaped secret %q: %s", secret, output.String())
		}
	}
}

func TestDaemonLogLevelCanChangeAtRuntime(t *testing.T) {
	var output bytes.Buffer
	level := &slog.LevelVar{}
	level.Set(slog.LevelWarn)
	logger := slog.New(NewJSONHandler(&output, level))
	logger.Info("hidden", Component("daemon"), Event("rpc_request_started"))
	if output.Len() != 0 {
		t.Fatalf("disabled info output = %q", output.String())
	}
	level.Set(slog.LevelDebug)
	logger.Debug("visible", Component("daemon"), Event("rpc_request_succeeded"))
	if !strings.Contains(output.String(), `"level":"DEBUG"`) {
		t.Fatalf("dynamic debug output = %q", output.String())
	}
}

func TestDefaultConfigFreezesDiagnosticResourceCeilings(t *testing.T) {
	want := Config{MaxBytes: 4 * 1024 * 1024, Backups: 3, RingCapacity: 1000}
	if got := DefaultConfig(); got != want {
		t.Fatalf("DefaultConfig() = %#v, want %#v", got, want)
	}
}

func TestOpenDaemonLogRotatesWithinBoundAndUsesPrivateModes(t *testing.T) {
	path := filepath.Join(testkit.PersistentTempDir(t), "private", "daemon.jsonl")
	log, err := OpenDaemon(path, Config{MaxBytes: 300, Backups: 2})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 30; index++ {
		log.Logger.Info("unsafe message", Component("daemon"), Event("rpc_request_succeeded"), RequestID(testRequestID))
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("stat %s: %v", candidate, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("mode %s = %o, want 600", candidate, info.Mode().Perm())
		}
		if info.Size() > 300 {
			t.Fatalf("size %s = %d, want <= 300", candidate, info.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: %v", err)
	}
}

func TestDaemonLogConcurrentWritesRemainBoundedJSON(t *testing.T) {
	path := filepath.Join(testkit.PersistentTempDir(t), "private", "daemon.jsonl")
	log, err := OpenDaemon(path, Config{MaxBytes: 1024, Backups: 2})
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for index := 0; index < 50; index++ {
				log.Logger.Info("unsafe", Component("daemon"), Event("rpc_request_started"), RequestID(testRequestID))
			}
		}()
	}
	writers.Wait()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{path, path + ".1", path + ".2"} {
		// #nosec G304 -- candidates are fixed suffixes below t.TempDir().
		file, err := os.Open(candidate)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				_ = file.Close()
				t.Fatalf("decode %s: %v", candidate, err)
			}
		}
		if err := errors.Join(scanner.Err(), file.Close()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenDaemonLogRejectsSymlinkDestination(t *testing.T) {
	root := testkit.PersistentTempDir(t)
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "daemon.jsonl")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenDaemon(path, Config{}); err == nil {
		t.Fatal("OpenDaemon() error = nil, want symlink rejection")
	}
	// #nosec G304 -- target is a test-owned path below t.TempDir().
	content, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "keep" {
		t.Fatalf("target content = %q", content)
	}
}
