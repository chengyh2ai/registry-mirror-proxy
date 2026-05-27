package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"registry-mirror/internal/secret"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr            string        `yaml:"listen_addr"`
	Upstream              string        `yaml:"upstream"`
	Upstreams             []string      `yaml:"upstreams"`
	UpstreamUsername      string        `yaml:"upstream_username"`
	UpstreamPassword      string        `yaml:"upstream_password"`
	UpstreamAuthEnabled   bool          `yaml:"upstream_auth_enabled"`
	UpstreamAccessKey     string        `yaml:"upstream_access_key"`
	UpstreamSecretKey     string        `yaml:"upstream_secret_key"`
	UpstreamRegion        string        `yaml:"upstream_region"`
	UpstreamEndpoint      string        `yaml:"upstream_endpoint"`
	UpstreamRegistry      string        `yaml:"upstream_registry"`
	UpstreamRefreshBefore time.Duration `yaml:"upstream_refresh_before"`
	TLSCertFile           string        `yaml:"tls_cert_file"`
	TLSKeyFile            string        `yaml:"tls_key_file"`
	ReadTimeout           time.Duration `yaml:"read_timeout"`
	WriteTimeout          time.Duration `yaml:"write_timeout"`
	IdleTimeout           time.Duration `yaml:"idle_timeout"`
	UpstreamTimeout       time.Duration `yaml:"upstream_timeout"`
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	LogLevel              string        `yaml:"log_level"`
	HideUpstreamErrors    bool          `yaml:"hide_upstream_errors"`
	AllowMethods          []string      `yaml:"allow_methods"`
	MaxRedirects          int           `yaml:"max_redirects"`
	EnableMetrics         bool          `yaml:"enable_metrics"`
	AllowedClientCIDRs    []string      `yaml:"allowed_client_cidrs"`
	EnableDiskCache       bool          `yaml:"enable_disk_cache"`
	DiskCacheDir          string        `yaml:"disk_cache_dir"`
	DiskCacheMaxBytes     int64         `yaml:"disk_cache_max_bytes"`
	EnableReadyCheck      bool          `yaml:"enable_ready_check"`
	TrustedRedirectHost   []string      `yaml:"trusted_redirect_hosts"`
	MaxConcurrentRequests int           `yaml:"max_concurrent_requests"`
}

func Defaults() Config {
	return Config{
		ListenAddr:            ":443",
		Upstream:              "",
		ReadTimeout:           30 * time.Second,
		WriteTimeout:          0,
		IdleTimeout:           120 * time.Second,
		UpstreamTimeout:       60 * time.Second,
		MaxIdleConns:          512,
		LogLevel:              "info",
		HideUpstreamErrors:    true,
		AllowMethods:          []string{"GET", "HEAD", "OPTIONS"},
		MaxRedirects:          5,
		EnableMetrics:         true,
		EnableReadyCheck:      true,
		DiskCacheDir:          "/var/cache/registry-mirror-proxy",
		MaxConcurrentRequests: 0,
		UpstreamRegion:        "",
		UpstreamEndpoint:      "",
		UpstreamRefreshBefore: 10 * time.Minute,
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return Config{}, err
		}
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return Config{}, err
		}
	}
	applyEnv(&cfg)
	if err := cfg.Decrypt(os.Getenv("REGISTRY_MIRROR_CONFIG_KEY")); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.ListenAddr == "" {
		return errors.New("listen_addr is required")
	}
	if c.Upstream != "" {
		c.Upstreams = append([]string{c.Upstream}, c.Upstreams...)
	}
	c.Upstreams = dedupe(c.Upstreams)
	if len(c.Upstreams) == 0 {
		return errors.New("upstream or upstreams is required")
	}
	for _, raw := range c.Upstreams {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("upstream must be an https URL: %q", raw)
		}
	}
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return errors.New("tls_cert_file and tls_key_file must be configured together")
	}
	if c.MaxIdleConns <= 0 {
		return errors.New("max_idle_conns must be greater than zero")
	}
	if c.MaxRedirects < 0 {
		return errors.New("max_redirects cannot be negative")
	}
	if len(c.AllowMethods) == 0 {
		return errors.New("allow_methods cannot be empty")
	}
	if c.UpstreamAuthEnabled {
		if c.UpstreamAccessKey == "" {
			return errors.New("upstream_access_key is required when upstream_auth_enabled is true")
		}
		if c.UpstreamSecretKey == "" {
			return errors.New("upstream_secret_key is required when upstream_auth_enabled is true")
		}
		if c.UpstreamRegistry == "" {
			return errors.New("upstream_registry is required when upstream_auth_enabled is true")
		}
		if c.UpstreamRegion == "" {
			return errors.New("upstream_region is required when upstream_auth_enabled is true")
		}
		if c.UpstreamEndpoint == "" {
			return errors.New("upstream_endpoint is required when upstream_auth_enabled is true")
		}
	}
	for _, cidr := range c.AllowedClientCIDRs {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid allowed_client_cidrs entry %q: %w", cidr, err)
		}
	}
	return nil
}

func (c *Config) Decrypt(key string) error {
	var err error
	decrypt := func(name string, value *string) error {
		if !secret.IsEncrypted(*value) {
			return nil
		}
		if key == "" {
			return fmt.Errorf("%s is encrypted but REGISTRY_MIRROR_CONFIG_KEY is not set", name)
		}
		*value, err = secret.Decrypt(*value, key)
		if err != nil {
			return fmt.Errorf("decrypt %s: %w", name, err)
		}
		return nil
	}
	for _, item := range []struct {
		name  string
		value *string
	}{
		{"upstream", &c.Upstream},
		{"upstream_username", &c.UpstreamUsername},
		{"upstream_password", &c.UpstreamPassword},
		{"upstream_access_key", &c.UpstreamAccessKey},
		{"upstream_secret_key", &c.UpstreamSecretKey},
		{"upstream_region", &c.UpstreamRegion},
		{"upstream_endpoint", &c.UpstreamEndpoint},
		{"upstream_registry", &c.UpstreamRegistry},
	} {
		if err := decrypt(item.name, item.value); err != nil {
			return err
		}
	}
	for i := range c.Upstreams {
		if secret.IsEncrypted(c.Upstreams[i]) {
			if key == "" {
				return fmt.Errorf("upstreams[%d] is encrypted but REGISTRY_MIRROR_CONFIG_KEY is not set", i)
			}
			c.Upstreams[i], err = secret.Decrypt(c.Upstreams[i], key)
			if err != nil {
				return fmt.Errorf("decrypt upstreams[%d]: %w", i, err)
			}
		}
	}
	return nil
}

func applyEnv(c *Config) {
	setString("REGISTRY_MIRROR_LISTEN_ADDR", &c.ListenAddr)
	setString("REGISTRY_MIRROR_UPSTREAM", &c.Upstream)
	setString("REGISTRY_MIRROR_UPSTREAM_USERNAME", &c.UpstreamUsername)
	setString("REGISTRY_MIRROR_UPSTREAM_PASSWORD", &c.UpstreamPassword)
	setString("REGISTRY_MIRROR_UPSTREAM_ACCESS_KEY", &c.UpstreamAccessKey)
	setString("REGISTRY_MIRROR_UPSTREAM_SECRET_KEY", &c.UpstreamSecretKey)
	setString("REGISTRY_MIRROR_UPSTREAM_REGION", &c.UpstreamRegion)
	setString("REGISTRY_MIRROR_UPSTREAM_ENDPOINT", &c.UpstreamEndpoint)
	setString("REGISTRY_MIRROR_UPSTREAM_REGISTRY", &c.UpstreamRegistry)
	setString("REGISTRY_MIRROR_TLS_CERT_FILE", &c.TLSCertFile)
	setString("REGISTRY_MIRROR_TLS_KEY_FILE", &c.TLSKeyFile)
	setString("REGISTRY_MIRROR_LOG_LEVEL", &c.LogLevel)
	setString("REGISTRY_MIRROR_DISK_CACHE_DIR", &c.DiskCacheDir)
	setBool("REGISTRY_MIRROR_HIDE_UPSTREAM_ERRORS", &c.HideUpstreamErrors)
	setBool("REGISTRY_MIRROR_ENABLE_METRICS", &c.EnableMetrics)
	setBool("REGISTRY_MIRROR_ENABLE_DISK_CACHE", &c.EnableDiskCache)
	setBool("REGISTRY_MIRROR_UPSTREAM_AUTH_ENABLED", &c.UpstreamAuthEnabled)
	setDuration("REGISTRY_MIRROR_READ_TIMEOUT", &c.ReadTimeout)
	setDuration("REGISTRY_MIRROR_WRITE_TIMEOUT", &c.WriteTimeout)
	setDuration("REGISTRY_MIRROR_IDLE_TIMEOUT", &c.IdleTimeout)
	setDuration("REGISTRY_MIRROR_UPSTREAM_TIMEOUT", &c.UpstreamTimeout)
	setDuration("REGISTRY_MIRROR_UPSTREAM_REFRESH_BEFORE", &c.UpstreamRefreshBefore)
	setInt("REGISTRY_MIRROR_MAX_IDLE_CONNS", &c.MaxIdleConns)
	setInt("REGISTRY_MIRROR_MAX_REDIRECTS", &c.MaxRedirects)
	setInt("REGISTRY_MIRROR_MAX_CONCURRENT_REQUESTS", &c.MaxConcurrentRequests)
	if v := os.Getenv("REGISTRY_MIRROR_UPSTREAMS"); v != "" {
		c.Upstreams = splitCSV(v)
	}
	if v := os.Getenv("REGISTRY_MIRROR_ALLOW_METHODS"); v != "" {
		c.AllowMethods = splitCSV(v)
	}
	if v := os.Getenv("REGISTRY_MIRROR_ALLOWED_CLIENT_CIDRS"); v != "" {
		c.AllowedClientCIDRs = splitCSV(v)
	}
}

func setString(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func setBool(key string, dst *bool) {
	if v := os.Getenv(key); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err == nil {
			*dst = parsed
		}
	}
}

func setInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		parsed, err := strconv.Atoi(v)
		if err == nil {
			*dst = parsed
		}
	}
}

func setDuration(key string, dst *time.Duration) {
	if v := os.Getenv(key); v != "" {
		parsed, err := time.ParseDuration(v)
		if err == nil {
			*dst = parsed
		}
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
