package redaction

import "testing"

func TestExportStringAppliesOneSharedSensitivityPolicy(t *testing.T) {
	secret := "stage6-secret-host.example/private/path?token=askpass-answer"
	tests := []struct {
		name        string
		class       Sensitivity
		value       string
		want        string
		wantInclude bool
	}{
		{name: "public token", class: Public, value: "daemon_running", want: "daemon_running", wantInclude: true},
		{name: "unsafe public value", class: Public, value: secret, want: Placeholder, wantInclude: true},
		{name: "system metadata", class: SystemMetadata, value: "go1.26.5", want: "go1.26.5", wantInclude: true},
		{name: "unsafe system metadata", class: SystemMetadata, value: secret, want: Placeholder, wantInclude: true},
		{name: "identifier", class: Pseudonymous, value: secret, want: Placeholder, wantInclude: true},
		{name: "secret", class: Secret, value: secret},
		{name: "content", class: Content, value: secret},
		{name: "unknown", class: Sensitivity("unknown"), value: secret},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, include := ExportString(test.class, test.value)
			if got != test.want || include != test.wantInclude {
				t.Fatalf("ExportString(%q, value) = (%q, %t), want (%q, %t)", test.class, got, include, test.want, test.wantInclude)
			}
			if got == secret {
				t.Fatal("export retained seeded secret")
			}
		})
	}
}

func TestSafeTokenAndSystemMetadataAreBounded(t *testing.T) {
	if !SafeToken("cache_unavailable") || SafeToken("") || SafeToken("Cache-Unavailable") || SafeToken(string(make([]byte, 65))) {
		t.Fatal("SafeToken policy does not match the frozen lower-snake-case bound")
	}
	if !SafeSystemMetadata("OpenSSH_9.9p1") || !SafeSystemMetadata("go1.26.5") {
		t.Fatal("expected bounded version metadata to be safe")
	}
	for _, unsafe := range []string{"user@example.com", "/Users/secret", "NAME=value", "has space", string(make([]byte, 129))} {
		if SafeSystemMetadata(unsafe) {
			t.Fatalf("SafeSystemMetadata(%q) = true", unsafe)
		}
	}
}
