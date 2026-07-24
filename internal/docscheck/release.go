package docscheck

// CheckRelease is retained as a compatibility alias for older local scripts.
// Public releases no longer have a separate Markdown checklist contract.
func CheckRelease(root string) []Finding {
	return Check(root)
}
