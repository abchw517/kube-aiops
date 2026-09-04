package config

import (
	"testing"

	"github.com/abchw517/kube-aiops/internal/security"
)

func TestLoadDefaultsSecurityModeToProduction(t *testing.T) {
	t.Setenv("SECURITY_MODE", "")
	t.Setenv("KUBERNETES_API_URL", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SecurityMode != security.ModeProduction {
		t.Fatalf("SecurityMode = %q, want production", cfg.SecurityMode)
	}
}

func TestLoadAcceptsExplicitDevelopmentSecurityMode(t *testing.T) {
	t.Setenv("SECURITY_MODE", "development")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.SecurityMode != security.ModeDevelopment {
		t.Fatalf("SecurityMode = %q, want development", cfg.SecurityMode)
	}
}

func TestLoadRejectsUnsupportedSecurityMode(t *testing.T) {
	t.Setenv("SECURITY_MODE", "permissive")
	if _, err := Load(); err == nil {
		t.Fatal("expected unsupported SECURITY_MODE rejection")
	}
}
