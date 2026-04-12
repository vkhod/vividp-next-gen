package license

// Checker gates ingestion and feature access per tenant/system.
// The real implementation (quota checks, expiry, module flags) lives here later.
// For now AlwaysGranted is the only implementation — the hook is in place.
type Checker interface {
	CanIngest(tenantID, systemID string) bool
	IsFeatureEnabled(tenantID, feature string) bool
}

// AlwaysGranted is the development stub — never denies anything.
type AlwaysGranted struct{}

func (AlwaysGranted) CanIngest(_, _ string) bool        { return true }
func (AlwaysGranted) IsFeatureEnabled(_, _ string) bool { return true }
