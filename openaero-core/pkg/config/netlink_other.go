//go:build !linux

package config

// CheckKernelRulesAndRoutes is a fallback stub for non-Linux platforms
func CheckKernelRulesAndRoutes(isFakeIP bool, fakeRange string) (bool, bool) {
	return false, false
}
