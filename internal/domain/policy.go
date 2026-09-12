package domain

// IntegrityPolicy is the requested content-verification policy shared by
// configuration and transfer planning.
type IntegrityPolicy string

const (
	IntegrityBaseline      IntegrityPolicy = "baseline"
	IntegrityStrong        IntegrityPolicy = "strong"
	IntegrityRequireStrong IntegrityPolicy = "require_strong"
	DefaultIntegrityPolicy                 = IntegrityStrong
)

// Production enhancements remain closed until their release trust and
// data-plane verification requirements are satisfied.
const (
	ProductionHelperOpen         = false
	ProductionDirectTransferOpen = false
)
