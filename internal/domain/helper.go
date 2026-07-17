package domain

// HelperArtifactID is the exact immutable Helper identity persisted by durable
// Jobs that depend on a Level 1 capability.
type HelperArtifactID struct {
	ProtocolMajor uint16 `json:"protocol_major"`
	Version       string `json:"version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	SHA256        string `json:"sha256"`
}
