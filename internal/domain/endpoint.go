package domain

import "time"

type EndpointKind string

const (
	EndpointLocal EndpointKind = "local"
	EndpointSSH   EndpointKind = "ssh"
)

type Endpoint struct {
	ID           EndpointID
	Kind         EndpointKind
	DisplayName  string
	SSHHostAlias string
}

type ConnectionState string

const (
	StateDisconnected ConnectionState = "disconnected"
	StateConnecting   ConnectionState = "connecting"
	StateReady        ConnectionState = "ready"
	StateDegraded     ConnectionState = "degraded"
	StateAuthRequired ConnectionState = "auth_required"
	StateFailed       ConnectionState = "failed"
)

type EndpointSnapshot struct {
	EndpointID   EndpointID
	SessionID    SessionID
	State        ConnectionState
	Capabilities CapabilitySnapshot
	ObservedAt   time.Time
}
