package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"registry-mirror/internal/secret"
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

func TestLoadDecryptsEncryptedValues(t *testing.T) {
	encryptedUpstream, err := secret.Encrypt("https://example.com", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	encryptedAK, err := secret.Encrypt("ak", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	encryptedSK, err := secret.Encrypt("sk", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	encryptedRegion, err := secret.Encrypt("cn-beijing", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	encryptedEndpoint, err := secret.Encrypt("https://auth.example.com", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	encryptedRegistry, err := secret.Encrypt("registrya", "test-key")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	err = os.WriteFile(path, []byte(`
listen_addr: ":8443"
upstream: "`+encryptedUpstream+`"
upstream_auth_enabled: true
upstream_access_key: "`+encryptedAK+`"
upstream_secret_key: "`+encryptedSK+`"
upstream_region: "`+encryptedRegion+`"
upstream_endpoint: "`+encryptedEndpoint+`"
upstream_registry: "`+encryptedRegistry+`"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REGISTRY_MIRROR_CONFIG_KEY", "test-key")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Upstream != "https://example.com" {
		t.Fatalf("upstream = %q", cfg.Upstream)
	}
	if cfg.UpstreamAccessKey != "ak" || cfg.UpstreamSecretKey != "sk" {
		t.Fatalf("credentials = %q %q", cfg.UpstreamAccessKey, cfg.UpstreamSecretKey)
	}
	if cfg.UpstreamRegistry != "registrya" {
		t.Fatalf("registry = %q", cfg.UpstreamRegistry)
	}
}
