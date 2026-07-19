package fake

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

var (
	errTreeNotFound     = errors.New("tree node not found")
	errTreeNotDirectory = errors.New("tree node is not a directory")
	errTreePermission   = errors.New("tree node permission denied")
	errSymlinkLoop      = errors.New("symlink loop")
	errSymlinkEscapes   = errors.New("symlink target escapes root")
)

// Node describes one caller-owned tree node in a Scenario.
type Node struct {
	Name       string
	Kind       domain.EntryKind
	Data       []byte
	Metadata   domain.Metadata
	LinkTarget string
	Children   []Node
}

type treeNode struct {
	id                uint64
	version           uint64
	listingGeneration uint64
	permissionDenied  bool
	name              string
	kind              domain.EntryKind
	data              []byte
	metadata          domain.Metadata
	linkTarget        string
	children          map[string]*treeNode
}

func buildReplacementNode(replacement Node, displayPath string) (*treeNode, uint64, error) {
	nextID := uint64(1)
	built, err := buildNode(
		&replacement,
		false,
		displayPath,
		&nextID,
		make(map[*Node]struct{}),
	)
	return built, nextID, err
}

func rebaseReplacementDescendantIDs(root *treeNode, firstID uint64) {
	for _, child := range root.children {
		rebaseReplacementNodeID(child, firstID)
	}
}

func rebaseReplacementNodeID(node *treeNode, firstID uint64) {
	node.id = firstID + (node.id - 2)
	for _, child := range node.children {
		rebaseReplacementNodeID(child, firstID)
	}
}

func buildTree(root Node) (*treeNode, error) {
	if root.Kind != domain.EntryDirectory {
		return nil, fmt.Errorf("create fake provider: root kind must be directory")
	}
	nextID := uint64(1)
	return buildNode(&root, true, "/", &nextID, make(map[*Node]struct{}))
}

func buildNode(
	node *Node,
	root bool,
	displayPath string,
	nextID *uint64,
	ancestors map[*Node]struct{},
) (*treeNode, error) {
	if _, cyclic := ancestors[node]; cyclic {
		return nil, fmt.Errorf("create fake provider: cyclic Node.Children at %q", displayPath)
	}
	ancestors[node] = struct{}{}
	defer delete(ancestors, node)

	if strings.IndexByte(node.Name, 0) >= 0 {
		return nil, fmt.Errorf("create fake provider: node %q name contains NUL", displayPath)
	}
	if !root {
		if node.Name == "" {
			return nil, fmt.Errorf("create fake provider: node %q has an empty name", displayPath)
		}
		if node.Name == "." || node.Name == ".." {
			return nil, fmt.Errorf("create fake provider: node %q uses a reserved name", displayPath)
		}
		if strings.Contains(node.Name, "/") {
			return nil, fmt.Errorf("create fake provider: node %q name contains a separator", displayPath)
		}
	}

	switch node.Kind {
	case domain.EntryDirectory:
	case domain.EntryFile, domain.EntryOther:
		if len(node.Children) != 0 {
			return nil, fmt.Errorf("create fake provider: non-directory %q has children", displayPath)
		}
	case domain.EntrySymlink:
		if len(node.Children) != 0 {
			return nil, fmt.Errorf("create fake provider: symlink %q has children", displayPath)
		}
		if node.LinkTarget == "" {
			return nil, fmt.Errorf("create fake provider: symlink %q has an empty target", displayPath)
		}
		if strings.IndexByte(node.LinkTarget, 0) >= 0 {
			return nil, fmt.Errorf("create fake provider: symlink %q target contains NUL", displayPath)
		}
	default:
		return nil, fmt.Errorf("create fake provider: node %q has invalid kind %q", displayPath, node.Kind)
	}

	owned := &treeNode{
		id:         *nextID,
		version:    1,
		name:       node.Name,
		kind:       node.Kind,
		data:       append([]byte(nil), node.Data...),
		metadata:   cloneMetadata(node.Metadata),
		linkTarget: node.LinkTarget,
	}
	*nextID = *nextID + 1
	if node.Kind == domain.EntryFile {
		size := uint64(len(owned.data))
		if owned.metadata.Size != nil && *owned.metadata.Size != size {
			return nil, fmt.Errorf(
				"create fake provider: file %q metadata size does not match data",
				displayPath,
			)
		}
		owned.metadata.Size = &size
	}
	if node.Kind == domain.EntryDirectory {
		owned.children = make(map[string]*treeNode, len(node.Children))
		owned.listingGeneration = 1
	}
	for index := range node.Children {
		child := &node.Children[index]
		if _, exists := owned.children[child.Name]; exists {
			return nil, fmt.Errorf(
				"create fake provider: directory %q has duplicate child name %q",
				displayPath,
				child.Name,
			)
		}
		childPath := "/" + child.Name
		if displayPath != "/" {
			childPath = displayPath + "/" + child.Name
		}
		built, err := buildNode(child, false, childPath, nextID, ancestors)
		if err != nil {
			return nil, err
		}
		owned.children[child.Name] = built
	}
	return owned, nil
}

func cloneMetadata(metadata domain.Metadata) domain.Metadata {
	cloned := metadata
	cloned.Size = clonePointer(metadata.Size)
	cloned.Mode = clonePointer(metadata.Mode)
	cloned.UID = clonePointer(metadata.UID)
	cloned.GID = clonePointer(metadata.GID)
	cloned.ModifiedAt = cloneTimeUTC(metadata.ModifiedAt)
	cloned.ModifiedPrecision = clonePointer(metadata.ModifiedPrecision)
	cloned.FileID = clonePointer(metadata.FileID)
	return cloned
}

func cloneFingerprint(fingerprint domain.Fingerprint) domain.Fingerprint {
	cloned := fingerprint
	cloned.Size = clonePointer(fingerprint.Size)
	cloned.ModifiedAt = cloneTimeUTC(fingerprint.ModifiedAt)
	cloned.ModifiedPrecision = clonePointer(fingerprint.ModifiedPrecision)
	cloned.FileID = clonePointer(fingerprint.FileID)
	cloned.VersionID = clonePointer(fingerprint.VersionID)
	cloned.HashAlgorithm = clonePointer(fingerprint.HashAlgorithm)
	cloned.HashHex = clonePointer(fingerprint.HashHex)
	return cloned
}

func cloneTimeUTC(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func resolveNode(
	root *treeNode,
	canonicalPath domain.CanonicalPath,
	followFinal bool,
) (*treeNode, error) {
	return resolveNodeSeen(root, canonicalPath, followFinal, make(map[domain.CanonicalPath]struct{}))
}

// resolveNodeWithPermission follows the same path semantics as resolveNode but
// reports the first denied node that is actually traversed. Authorization
// probes deliberately ignore every other error so existing lookup behavior is
// left to the operation's normal resolution path.
func resolveNodeWithPermission(
	root *treeNode,
	canonicalPath domain.CanonicalPath,
	followFinal bool,
) (*treeNode, error) {
	return resolveNodeWithPermissionSeen(
		root,
		canonicalPath,
		followFinal,
		make(map[domain.CanonicalPath]struct{}),
	)
}

func resolveNodeWithPermissionSeen(
	root *treeNode,
	canonicalPath domain.CanonicalPath,
	followFinal bool,
	seen map[domain.CanonicalPath]struct{},
) (*treeNode, error) {
	current := root
	currentPath := domain.CanonicalPath("/")
	if current.permissionDenied {
		return nil, fmt.Errorf("%w: %s", errTreePermission, currentPath)
	}
	components := splitCanonical(string(canonicalPath))
	for index, component := range components {
		if current.kind != domain.EntryDirectory {
			return nil, fmt.Errorf("%w: %s", errTreeNotDirectory, currentPath)
		}
		child, ok := current.children[component]
		if !ok {
			return nil, fmt.Errorf("%w: %s", errTreeNotFound, canonicalPath)
		}
		childPath := joinCanonical(currentPath, component)
		if child.permissionDenied {
			return nil, fmt.Errorf("%w: %s", errTreePermission, childPath)
		}
		isFinal := index == len(components)-1
		if child.kind == domain.EntrySymlink && (!isFinal || followFinal) {
			if _, repeated := seen[childPath]; repeated || len(seen) >= 64 {
				return nil, fmt.Errorf("%w: %s", errSymlinkLoop, childPath)
			}
			seen[childPath] = struct{}{}
			targetPath, err := resolveLinkTarget(currentPath, child.linkTarget)
			if err != nil {
				return nil, err
			}
			remaining := components[index+1:]
			if len(remaining) != 0 {
				combined := append(splitCanonical(string(targetPath)), remaining...)
				targetPath = domain.CanonicalPath("/" + strings.Join(combined, "/"))
			}
			return resolveNodeWithPermissionSeen(root, targetPath, followFinal, seen)
		}
		current = child
		currentPath = childPath
	}
	return current, nil
}

func resolveNodeSeen(
	root *treeNode,
	canonicalPath domain.CanonicalPath,
	followFinal bool,
	seen map[domain.CanonicalPath]struct{},
) (*treeNode, error) {
	components := splitCanonical(string(canonicalPath))
	current := root
	currentPath := domain.CanonicalPath("/")
	for index, component := range components {
		if current.kind != domain.EntryDirectory {
			return nil, fmt.Errorf("%w: %s", errTreeNotDirectory, currentPath)
		}
		child, ok := current.children[component]
		if !ok {
			return nil, fmt.Errorf("%w: %s", errTreeNotFound, canonicalPath)
		}
		childPath := joinCanonical(currentPath, component)
		isFinal := index == len(components)-1
		if child.kind == domain.EntrySymlink && (!isFinal || followFinal) {
			if _, repeated := seen[childPath]; repeated || len(seen) >= 64 {
				return nil, fmt.Errorf("%w: %s", errSymlinkLoop, childPath)
			}
			seen[childPath] = struct{}{}
			targetPath, err := resolveLinkTarget(currentPath, child.linkTarget)
			if err != nil {
				return nil, err
			}
			remaining := components[index+1:]
			if len(remaining) != 0 {
				combined := append(splitCanonical(string(targetPath)), remaining...)
				targetPath = domain.CanonicalPath("/" + strings.Join(combined, "/"))
			}
			return resolveNodeSeen(root, targetPath, followFinal, seen)
		}
		current = child
		currentPath = childPath
	}
	return current, nil
}

func resolveLinkTarget(
	parent domain.CanonicalPath,
	target string,
) (domain.CanonicalPath, error) {
	components := splitCanonical(string(parent))
	if strings.HasPrefix(target, "/") {
		components = nil
	}
	normalized, err := applyPath(components, target)
	if err != nil {
		return "", fmt.Errorf("%w: %s", errSymlinkEscapes, err.Error())
	}
	if len(normalized) == 0 {
		return "/", nil
	}
	return domain.CanonicalPath("/" + strings.Join(normalized, "/")), nil
}

func joinCanonical(parent domain.CanonicalPath, name string) domain.CanonicalPath {
	if parent == "/" {
		return domain.CanonicalPath("/" + name)
	}
	return domain.CanonicalPath(string(parent) + "/" + name)
}

func baseName(path domain.CanonicalPath) string {
	if path == "/" {
		return "/"
	}
	components := splitCanonical(string(path))
	return components[len(components)-1]
}

func parentPath(path domain.CanonicalPath) (domain.CanonicalPath, string, error) {
	components := splitCanonical(string(path))
	if len(components) == 0 {
		return "", "", fmt.Errorf("root has no parent")
	}
	name := components[len(components)-1]
	components = components[:len(components)-1]
	if len(components) == 0 {
		return "/", name, nil
	}
	return domain.CanonicalPath("/" + strings.Join(components, "/")), name, nil
}

func maxNodeID(node *treeNode) uint64 {
	maximum := node.id
	for _, child := range node.children {
		if childMaximum := maxNodeID(child); childMaximum > maximum {
			maximum = childMaximum
		}
	}
	return maximum
}

func findNodePath(
	current *treeNode,
	target *treeNode,
	currentPath domain.CanonicalPath,
) (domain.CanonicalPath, bool) {
	if current == target {
		return currentPath, true
	}
	if current.kind != domain.EntryDirectory {
		return "", false
	}
	for name, child := range current.children {
		if found, ok := findNodePath(child, target, joinCanonical(currentPath, name)); ok {
			return found, true
		}
	}
	return "", false
}

func containsNode(root *treeNode, target *treeNode) bool {
	if root == target {
		return true
	}
	if root.kind != domain.EntryDirectory {
		return false
	}
	for _, child := range root.children {
		if containsNode(child, target) {
			return true
		}
	}
	return false
}
