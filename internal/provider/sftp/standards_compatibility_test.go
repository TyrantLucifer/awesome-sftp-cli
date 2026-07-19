package sftp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
	pkgsftp "github.com/pkg/sftp"
)

func TestStandardsCompatibleVirtualServerKeepsLevelZeroReadPath(t *testing.T) {
	implementation, client := newStandardsCompatibleVirtualProvider(t)
	if err := client.Mkdir("/documents"); err != nil {
		t.Fatal(err)
	}
	writeVirtualSFTPFile(t, client, "/documents/readme.txt", []byte("portable SFTP v3"))

	for _, extension := range []string{"fsync@openssh.com", "hardlink@openssh.com", "posix-rename@openssh.com"} {
		if value, ok := client.HasExtension(extension); ok {
			t.Fatalf("virtual server unexpectedly advertised %s=%q", extension, value)
		}
	}
	snapshot, err := implementation.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Capabilities.Lookup("read"); !ok {
		t.Fatal("standard SFTP read capability is absent")
	}
	if _, ok := snapshot.Capabilities.Lookup("write"); ok {
		t.Fatal("extension-free server advertised mutation-safe write capability")
	}

	directory := domain.Location{EndpointID: testEndpointID, Path: "/documents"}
	page, err := implementation.List(context.Background(), providerapi.ListRequest{Location: directory, Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Name != "readme.txt" || !page.Done {
		t.Fatalf("List() = %#v, want one complete standard-v3 entry", page)
	}
	assertSFTPRead(t, implementation, page.Entries[0].Location, []byte("portable SFTP v3"))
}

func TestStandardsCompatibleVirtualServerRejectsUnsafePublicationWithoutMutation(t *testing.T) {
	implementation, client := newStandardsCompatibleVirtualProvider(t)
	writeVirtualSFTPFile(t, client, "/source.part", []byte("complete bytes"))

	_, err := implementation.Rename(context.Background(), providerapi.RenameRequest{
		Source:      domain.Location{EndpointID: testEndpointID, Path: "/source.part"},
		Destination: domain.Location{EndpointID: testEndpointID, Path: "/published"},
	})
	var operationError *domain.OpError
	if !errors.As(err, &operationError) || operationError.Code != domain.CodeUnsupported || operationError.Effect != domain.EffectNone {
		t.Fatalf("Rename() error = %#v, want unsupported with no effect", err)
	}
	if data := readVirtualSFTPFile(t, client, "/source.part"); !bytes.Equal(data, []byte("complete bytes")) {
		t.Fatalf("source after rejected publication = %q", data)
	}
	if _, statErr := client.Lstat("/published"); !isSFTPNotExist(statErr) {
		t.Fatalf("destination after rejected publication error = %v, want absent", statErr)
	}
}

func TestStandardsCompatibleVirtualServerPreservesInvalidUTF8NameBytes(t *testing.T) {
	implementation, client := newStandardsCompatibleVirtualProvider(t)
	rawName := string([]byte{'r', 'a', 'w', '-', 0xff, '-', 0xfe})
	rawPath := "/" + rawName
	want := []byte("raw-name payload")
	writeVirtualSFTPFile(t, client, rawPath, want)

	root := domain.Location{EndpointID: testEndpointID, Path: "/"}
	page, err := implementation.List(context.Background(), providerapi.ListRequest{Location: root, Limit: 16})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 {
		t.Fatalf("List() entries = %#v, want one raw-name entry", page.Entries)
	}
	entry := page.Entries[0]
	if !bytes.Equal([]byte(entry.Name), []byte(rawName)) || !bytes.Equal([]byte(entry.Location.Path), []byte(rawPath)) {
		t.Fatalf("raw identity changed: name=%x path=%x", []byte(entry.Name), []byte(entry.Location.Path))
	}
	stated, err := implementation.Stat(context.Background(), providerapi.StatRequest{Location: entry.Location})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(stated.Name), []byte(rawName)) || !equalFingerprint(stated.Fingerprint, entry.Fingerprint) {
		t.Fatalf("Stat() identity = %#v, want exact listed raw identity", stated)
	}
	assertSFTPRead(t, implementation, entry.Location, want)
}

func newStandardsCompatibleVirtualProvider(t *testing.T) (*Provider, *pkgsftp.Client) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestStandardsCompatibleVirtualServerHelperProcess$", "--") //nolint:gosec // exact current test binary from os.Executable.
	command.Env = append(os.Environ(), "AMSFTP_TEST_STANDARD_SFTP_SERVER=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- command.Wait() }()
	client, err := pkgsftp.NewClientPipe(stdout, stdin)
	if err != nil {
		_ = stdin.Close()
		<-processDone
		t.Fatal(err)
	}
	implementation, err := New(Config{
		Endpoint:  domain.Endpoint{ID: testEndpointID, Kind: domain.EndpointSSH, DisplayName: "standard-v3", SSHHostAlias: "standard-v3"},
		SessionID: testSessionID,
		Client:    client,
		Root:      "/",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = implementation.Close()
		select {
		case processErr := <-processDone:
			if processErr != nil {
				t.Errorf("virtual SFTP server: %v: %s", processErr, stderr.String())
			}
		case <-time.After(time.Second):
			_ = command.Process.Kill()
			<-processDone
			t.Error("virtual SFTP server did not stop")
		}
	})
	return implementation, client
}

func TestStandardsCompatibleVirtualServerHelperProcess(t *testing.T) {
	if os.Getenv("AMSFTP_TEST_STANDARD_SFTP_SERVER") != "1" {
		return
	}
	if err := pkgsftp.SetSFTPExtensions(); err != nil {
		os.Exit(2)
	}
	server := pkgsftp.NewRequestServer(standardSFTPStdio{}, pkgsftp.InMemHandler())
	if err := server.Serve(); err != nil && !errors.Is(err, io.EOF) {
		os.Exit(3)
	}
	os.Exit(0)
}

type standardSFTPStdio struct{}

func (standardSFTPStdio) Read(value []byte) (int, error)  { return os.Stdin.Read(value) }
func (standardSFTPStdio) Write(value []byte) (int, error) { return os.Stdout.Write(value) }
func (standardSFTPStdio) Close() error                    { return nil }

func assertSFTPRead(t *testing.T, implementation *Provider, location domain.Location, want []byte) {
	t.Helper()
	handle, err := implementation.OpenRead(context.Background(), providerapi.OpenReadRequest{Location: location})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := handle.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	var got bytes.Buffer
	buffer := make([]byte, 7)
	for {
		n, readErr := handle.Read(context.Background(), buffer)
		got.Write(buffer[:n])
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("read bytes = %q, want %q", got.Bytes(), want)
	}
}

func writeVirtualSFTPFile(t *testing.T, client *pkgsftp.Client, path string, data []byte) {
	t.Helper()
	file, err := client.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func readVirtualSFTPFile(t *testing.T, client *pkgsftp.Client, path string) []byte {
	t.Helper()
	file, err := client.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return data
}
