package providers

import (
	"os"
	"testing"
)

func TestUnsupportedProviderModeFailsClosed(t *testing.T) {
	t.Setenv("CENTERSEAT_PROVIDER_MODE", "fixture")
	if _, _, err := FromEnvironment(); err == nil {
		t.Fatal("expected non-licensed provider configuration to be rejected")
	}
}

func TestProviderModeIsRequired(t *testing.T) {
	_ = os.Unsetenv("CENTERSEAT_PROVIDER_MODE")
	if _, _, err := FromEnvironment(); err == nil {
		t.Fatal("expected missing provider mode to fail closed")
	}
}

func TestAtomModeRequiresAPIKey(t *testing.T) {
	t.Setenv("CENTERSEAT_PROVIDER_MODE", "atom")
	t.Setenv("ATOM_API_KEY", "")
	if _, _, err := FromEnvironment(); err == nil {
		t.Fatal("expected missing Atom key to fail before startup")
	}
}

func TestOpenCinemaModeRequiresAPIKey(t *testing.T) {
	t.Setenv("CENTERSEAT_PROVIDER_MODE", "opencinema")
	t.Setenv("OPEN_CINEMA_API_KEY", "")
	if _, _, err := FromEnvironment(); err == nil {
		t.Fatal("expected missing Open Cinema key to fail before startup")
	}
}
