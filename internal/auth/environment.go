package auth

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
)

const (
	EnvInternalRole = "AMSFTP_INTERNAL_ROLE"
	// #nosec G101 -- this is an environment variable name, not a credential.
	EnvAttemptToken = "AMSFTP_BROKER_TOKEN"
)

type InternalRole string

const InternalRoleAskpass InternalRole = "askpass"

func OpenSSHEnvironment(base []string, executable string, token Token) ([]string, error) {
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || strings.IndexByte(executable, 0) >= 0 {
		return nil, errors.New("create OpenSSH environment: askpass executable must be canonical absolute")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(string(token))
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("create OpenSSH environment: invalid attempt token")
	}
	protected := map[string]struct{}{
		"SSH_ASKPASS": {}, "SSH_ASKPASS_REQUIRE": {}, "DISPLAY": {}, EnvInternalRole: {}, EnvAttemptToken: {},
	}
	result := make([]string, 0, len(base)+5)
	for _, entry := range base {
		key, _, _ := strings.Cut(entry, "=")
		if _, replace := protected[key]; !replace {
			result = append(result, entry)
		}
	}
	return append(result,
		"SSH_ASKPASS="+executable,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=amsftp",
		EnvInternalRole+"="+string(InternalRoleAskpass),
		EnvAttemptToken+"="+string(token),
	), nil
}
