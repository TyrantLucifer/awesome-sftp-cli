package fake

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

// Controller applies deterministic state changes to one Provider instance.
type Controller struct {
	provider *Provider
}

// Calls returns an owned snapshot of structurally valid Provider operations.
func (c *Controller) Calls() []Call {
	if c == nil || c.provider == nil || c.provider.script == nil {
		return nil
	}
	return c.provider.script.callsCopy()
}

// Advance moves the fixture's deterministic clock. It panics when the injected
// clock is not manually advanceable because the Controller API has no error
// result and silently ignoring the request would make tests nondeterministic.
func (c *Controller) Advance(duration time.Duration) {
	if c == nil || c.provider == nil {
		panic("fake Controller.Advance: controller is not attached")
	}
	clock, ok := c.provider.clock.(interface{ Advance(time.Duration) })
	if !ok {
		panic(fmt.Sprintf(
			"fake Controller.Advance: clock %T does not support Advance(time.Duration)",
			c.provider.clock,
		))
	}
	clock.Advance(duration)
}

// ReleaseGate opens a predeclared one-shot gate. Release is sticky and
// idempotent, and it never acquires Provider or handle locks.
func (c *Controller) ReleaseGate(name string) {
	if c == nil || c.provider == nil || c.provider.script == nil {
		panic("fake Controller.ReleaseGate: controller is not attached")
	}
	if name == "" {
		panic("fake Controller.ReleaseGate: gate name is empty")
	}
	if !c.provider.script.releaseGate(name) {
		panic(fmt.Sprintf("fake Controller.ReleaseGate: unknown gate %q", name))
	}
}

// SetPermission changes the authorization state of the resolved node identity.
// Lookup intentionally bypasses permission checks so denied roots and ancestors
// can always be restored through the Controller.
func (c *Controller) SetPermission(path domain.CanonicalPath, allowed bool) error {
	if c == nil || c.provider == nil {
		return detachedControllerError("set_permission")
	}
	p := c.provider
	location := domain.Location{EndpointID: p.endpoint.ID, Path: path}
	canonical, err := canonicalizeAbsolute(string(path))
	if err != nil || canonical != string(path) {
		return p.error(
			domain.CodeInvalidArgument,
			"set_permission",
			&location,
			"permission location must be a canonical path",
			err,
		)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	node, err := resolveNode(p.root, path, true)
	if err != nil {
		return p.lookupError("set_permission", &location, err)
	}
	node.permissionDenied = !allowed
	return nil
}

// ReplaceNode atomically replaces one lexical dirent while preserving the
// existing node's identity. It is a deterministic test control, not a Provider
// operation, so it does not add a Call record or consume a fault step.
func (c *Controller) ReplaceNode(path domain.CanonicalPath, replacement Node) error {
	if c == nil || c.provider == nil {
		return &domain.OpError{
			Code:      domain.CodeInternal,
			Message:   "fake controller is not attached",
			Operation: "replace_node",
			Retry:     domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:    domain.EffectNone,
		}
	}
	p := c.provider
	location := domain.Location{EndpointID: p.endpoint.ID, Path: path}
	canonical, err := canonicalizeAbsolute(string(path))
	if err != nil || canonical != string(path) || path == "/" {
		return p.error(
			domain.CodeInvalidArgument,
			"replace_node",
			&location,
			"replacement location must be a canonical non-root path",
			err,
		)
	}
	if replacement.Name != baseName(path) {
		return p.error(
			domain.CodeInvalidArgument,
			"replace_node",
			&location,
			"replacement root name does not match location basename",
			nil,
		)
	}

	detached, localNextID, err := buildReplacementNode(replacement, string(path))
	if err != nil {
		return p.error(
			domain.CodeInvalidArgument,
			"replace_node",
			&location,
			"replacement node is invalid",
			err,
		)
	}
	parentLocation, name, err := parentPath(path)
	if err != nil {
		return p.error(
			domain.CodeInvalidArgument,
			"replace_node",
			&location,
			"replacement location has no parent",
			err,
		)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	parent, err := resolveNode(p.root, parentLocation, true)
	if err != nil || parent.kind != domain.EntryDirectory {
		if err == nil {
			err = errTreeNotDirectory
		}
		return p.lookupError("replace_node", &location, err)
	}
	existing, exists := parent.children[name]
	if !exists {
		return p.lookupError("replace_node", &location, errTreeNotFound)
	}
	if existing.kind != detached.kind {
		return p.error(
			domain.CodeInvalidArgument,
			"replace_node",
			&location,
			"replacement root kind does not match existing node",
			nil,
		)
	}

	descendantCount := localNextID - 2
	if descendantCount > ^uint64(0)-p.nextNodeID {
		return p.error(
			domain.CodeResourceExhausted,
			"replace_node",
			&location,
			"replacement node IDs are exhausted",
			nil,
		)
	}
	candidateNextID := p.nextNodeID + descendantCount
	rebaseReplacementDescendantIDs(detached, p.nextNodeID)

	existingID := existing.id
	existingVersion := existing.version
	existingGeneration := existing.listingGeneration
	existingPermissionDenied := existing.permissionDenied
	*existing = *detached
	existing.id = existingID
	existing.version = existingVersion
	existing.listingGeneration = existingGeneration
	existing.permissionDenied = existingPermissionDenied

	if existing.kind == domain.EntryDirectory {
		p.bumpDirectoryLocked(existing)
	} else {
		existing.version++
		p.recordHistoryLocked(existing, &path)
	}
	p.bumpDirectoryLocked(parent)
	for childName, child := range existing.children {
		p.seedHistoryLocked(child, joinCanonical(path, childName))
	}
	p.nextNodeID = candidateNextID
	return nil
}

// InvalidateListing advances only the addressed directory's listing generation.
func (c *Controller) InvalidateListing(ctx context.Context, location domain.Location) error {
	if c == nil || c.provider == nil {
		return &domain.OpError{
			Code:      domain.CodeInternal,
			Message:   "fake controller is not attached",
			Operation: "invalidate_listing",
			Retry:     domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:    domain.EffectNone,
		}
	}
	p := c.provider
	if err := p.checkContext(ctx, "invalidate_listing", &location); err != nil {
		return err
	}
	canonical, err := canonicalizeAbsolute(string(location.Path))
	if location.EndpointID != p.endpoint.ID || err != nil || canonical != string(location.Path) {
		return p.error(
			domain.CodeInvalidArgument,
			"invalidate_listing",
			&location,
			"location does not identify a canonical path on this provider",
			err,
		)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "invalidate_listing", &location); err != nil {
		return err
	}
	directory, err := resolveNode(p.root, location.Path, true)
	if err != nil {
		return p.lookupError("invalidate_listing", &location, err)
	}
	if directory.kind != domain.EntryDirectory {
		return p.error(
			domain.CodeInvalidArgument,
			"invalidate_listing",
			&location,
			"location is not a directory",
			nil,
		)
	}
	directory.listingGeneration++
	return nil
}

// SetSnapshot atomically replaces the endpoint snapshot after validating its
// session-scoped capability revision. It is a deterministic test control and
// therefore does not add a Provider Call record or consume a fault step.
func (c *Controller) SetSnapshot(snapshot domain.EndpointSnapshot) error {
	if c == nil || c.provider == nil {
		return detachedControllerError("set_snapshot")
	}
	p := c.provider
	if snapshot.EndpointID != p.endpoint.ID {
		return p.error(
			domain.CodeInvalidArgument,
			"set_snapshot",
			nil,
			"snapshot endpoint does not match provider",
			nil,
		)
	}
	if snapshot.SessionID == "" {
		return p.error(
			domain.CodeInvalidArgument,
			"set_snapshot",
			nil,
			"snapshot session ID is empty",
			nil,
		)
	}
	if !isKnownConnectionState(snapshot.State) {
		return p.error(
			domain.CodeInvalidArgument,
			"set_snapshot",
			nil,
			"snapshot connection state is invalid",
			nil,
		)
	}
	if snapshot.Capabilities.Revision.SessionID != snapshot.SessionID {
		return p.error(
			domain.CodeInvalidArgument,
			"set_snapshot",
			nil,
			"capability revision does not match snapshot session",
			nil,
		)
	}
	capabilities, err := ownCapabilitySnapshot(snapshot.Capabilities)
	if err != nil {
		return p.error(
			domain.CodeInvalidArgument,
			"set_snapshot",
			nil,
			"capability payload is invalid",
			err,
		)
	}
	owned := snapshot
	owned.Capabilities = capabilities

	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.snapshot
	sameSession := owned.SessionID == current.SessionID
	if sameSession {
		currentGeneration := current.Capabilities.Revision.Generation
		nextGeneration := owned.Capabilities.Revision.Generation
		if nextGeneration < currentGeneration {
			return p.error(
				domain.CodeInvalidArgument,
				"set_snapshot",
				nil,
				"capability generation moved backwards within one session",
				nil,
			)
		}
		if nextGeneration == currentGeneration &&
			!equalCapabilityPayload(current.Capabilities, owned.Capabilities) {
			return p.error(
				domain.CodeInvalidArgument,
				"set_snapshot",
				nil,
				"equal capability generation changed its payload",
				nil,
			)
		}
	} else if p.sessionEpoch == ^uint64(0) {
		return p.error(
			domain.CodeResourceExhausted,
			"set_snapshot",
			nil,
			"provider session epoch is exhausted",
			nil,
		)
	}

	if !sameSession {
		p.sessionEpoch++
		p.capabilitySeen, p.capabilityLost = capabilityHistoryForNewSession(owned.Capabilities)
		clear(p.cursors)
	} else if owned.Capabilities.Revision.Generation > current.Capabilities.Revision.Generation {
		p.capabilitySeen, p.capabilityLost = reconciledCapabilityHistory(
			p.capabilitySeen,
			p.capabilityLost,
			owned.Capabilities,
		)
	}
	if sameSession && stateClearsCursors(owned.State) {
		clear(p.cursors)
	}
	p.snapshot = owned
	return nil
}

// SetCapabilities installs a strictly newer capability snapshot for the
// current session. Clock sampling and input ownership happen outside Provider
// locks so injected clocks may reenter Snapshot safely.
func (c *Controller) SetCapabilities(capabilities domain.CapabilitySnapshot) error {
	if c == nil || c.provider == nil {
		return detachedControllerError("set_capabilities")
	}
	p := c.provider
	owned, err := ownCapabilitySnapshot(capabilities)
	if err != nil {
		return p.error(
			domain.CodeInvalidArgument,
			"set_capabilities",
			nil,
			"capability payload is invalid",
			err,
		)
	}
	observedAt := p.clock.Now()

	p.mu.Lock()
	defer p.mu.Unlock()
	current := p.snapshot.Capabilities.Revision
	if owned.Revision.SessionID != p.snapshot.SessionID {
		return p.error(
			domain.CodeInvalidArgument,
			"set_capabilities",
			nil,
			"capability session does not match current provider session",
			nil,
		)
	}
	if owned.Revision.Generation <= current.Generation {
		return p.error(
			domain.CodeInvalidArgument,
			"set_capabilities",
			nil,
			"capability generation must be strictly newer",
			nil,
		)
	}
	p.applyCapabilitiesLocked(owned, observedAt)
	return nil
}

// ChangeCapabilities replaces the capability payload and advances its generation
// without changing the provider session.
func (c *Controller) ChangeCapabilities(
	ctx context.Context,
	complete bool,
	items []domain.Capability,
) error {
	if c == nil || c.provider == nil {
		return &domain.OpError{
			Code:      domain.CodeInternal,
			Message:   "fake controller is not attached",
			Operation: "change_capabilities",
			Retry:     domain.RetryAdvice{Kind: domain.RetryNever},
			Effect:    domain.EffectNone,
		}
	}
	p := c.provider
	if err := p.checkContext(ctx, "change_capabilities", nil); err != nil {
		return err
	}
	ownedItems, err := ownCapabilityItems(items)
	if err != nil {
		return p.error(
			domain.CodeInvalidArgument,
			"change_capabilities",
			nil,
			"capability payload is invalid",
			err,
		)
	}
	observedAt := p.clock.Now()
	if err := p.checkContext(ctx, "change_capabilities", nil); err != nil {
		return err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkContext(ctx, "change_capabilities", nil); err != nil {
		return err
	}
	revision := p.snapshot.Capabilities.Revision
	if revision.Generation == ^uint64(0) {
		return p.error(
			domain.CodeResourceExhausted,
			"change_capabilities",
			nil,
			"capability generation is exhausted",
			nil,
		)
	}
	revision.Generation++
	capabilities := domain.CapabilitySnapshot{
		Revision: revision,
		Complete: complete,
		Items:    ownedItems,
	}
	p.applyCapabilitiesLocked(capabilities, observedAt)
	return nil
}

func detachedControllerError(operation string) error {
	return &domain.OpError{
		Code:      domain.CodeInternal,
		Message:   "fake controller is not attached",
		Operation: operation,
		Retry:     domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:    domain.EffectNone,
	}
}

func ownCapabilitySnapshot(snapshot domain.CapabilitySnapshot) (domain.CapabilitySnapshot, error) {
	return domain.NewCapabilitySnapshot(snapshot.Revision, snapshot.Complete, snapshot.Items)
}

func ownCapabilityItems(items []domain.Capability) ([]domain.Capability, error) {
	const validationSession domain.SessionID = "sess_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	validated, err := domain.NewCapabilitySnapshot(
		domain.CapabilityRevision{SessionID: validationSession, Generation: 1},
		false,
		items,
	)
	if err != nil {
		return nil, err
	}
	return validated.Items, nil
}

func equalCapabilityPayload(left domain.CapabilitySnapshot, right domain.CapabilitySnapshot) bool {
	return left.Complete == right.Complete && reflect.DeepEqual(left.Items, right.Items)
}

func isKnownConnectionState(state domain.ConnectionState) bool {
	switch state {
	case domain.StateDisconnected,
		domain.StateConnecting,
		domain.StateReady,
		domain.StateDegraded,
		domain.StateAuthRequired,
		domain.StateFailed:
		return true
	default:
		return false
	}
}

func stateClearsCursors(state domain.ConnectionState) bool {
	switch state {
	case domain.StateConnecting,
		domain.StateDisconnected,
		domain.StateAuthRequired,
		domain.StateFailed:
		return true
	default:
		return false
	}
}

func capabilityHistoryForNewSession(
	capabilities domain.CapabilitySnapshot,
) (map[domain.CapabilityName]struct{}, map[domain.CapabilityName]struct{}) {
	return reconciledCapabilityHistory(
		make(map[domain.CapabilityName]struct{}),
		make(map[domain.CapabilityName]struct{}),
		capabilities,
	)
}

func reconciledCapabilityHistory(
	seen map[domain.CapabilityName]struct{},
	lost map[domain.CapabilityName]struct{},
	capabilities domain.CapabilitySnapshot,
) (map[domain.CapabilityName]struct{}, map[domain.CapabilityName]struct{}) {
	nextSeen := cloneCapabilityNameSet(seen)
	nextLost := cloneCapabilityNameSet(lost)
	present := make(map[domain.CapabilityName]struct{}, len(capabilities.Items))
	for _, capability := range capabilities.Items {
		present[capability.Name] = struct{}{}
		nextSeen[capability.Name] = struct{}{}
		delete(nextLost, capability.Name)
	}
	if capabilities.Complete {
		for name := range nextSeen {
			if _, ok := present[name]; !ok {
				nextLost[name] = struct{}{}
			}
		}
	}
	return nextSeen, nextLost
}

func cloneCapabilityNameSet(
	source map[domain.CapabilityName]struct{},
) map[domain.CapabilityName]struct{} {
	cloned := make(map[domain.CapabilityName]struct{}, len(source))
	for name := range source {
		cloned[name] = struct{}{}
	}
	return cloned
}

func (p *Provider) applyCapabilitiesLocked(
	capabilities domain.CapabilitySnapshot,
	observedAt time.Time,
) {
	p.capabilitySeen, p.capabilityLost = reconciledCapabilityHistory(
		p.capabilitySeen,
		p.capabilityLost,
		capabilities,
	)
	p.snapshot.Capabilities = capabilities
	p.snapshot.ObservedAt = observedAt
}
