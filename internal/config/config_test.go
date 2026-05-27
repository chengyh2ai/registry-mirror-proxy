package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigWithDefaultsAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err := os.WriteFile(path, []byte(`
listen_addr: ":8443"
upstream: "https://example.com"
read_timeout: 5s
allow_methods: [GET, HEAD]
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REGISTRY_MIRROR_LOG_LEVEL", "debug")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":8443" {
		t.Fatalf("listen_addr = %q", cfg.ListenAddr)
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Fatalf("read_timeout = %s", cfg.ReadTimeout)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("log_level = %q", cfg.LogLevel)
	}
	if cfg.Upstreams[0] != "https://example.com" {
		t.Fatalf("upstreams = %#v", cfg.Upstreams)
	}
}

func TestValidateRejectsInvalidCIDR(t *testing.T) {
	cfg := Defaults()
	cfg.AllowedClientCIDRs = []string{"not-a-cidr"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid cidr error")
	}
}
