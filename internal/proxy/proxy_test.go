package proxy

import (
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"registry-mirror/internal/config"
)

func newTestProxy(t *testing.T, upstream *httptest.Server, mutate func(*config.Config)) *Proxy {
	t.Helper()
	cfg := config.Defaults()
	cfg.Upstream = upstream.URL
	cfg.Upstreams = nil
	cfg.TLSCertFile = ""
	cfg.TLSKeyFile = ""
	cfg.EnableMetrics = true
	if mutate != nil {
		mutate(&cfg)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	p, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	p.client.Transport = upstream.Client().Transport
	return p
}

func TestProxyForwardsV2AndHidesUpstreamHost(t *testing.T) {
	var seenHost string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+r.Host+`/token"`)
		w.Header().Set("Location", `https://`+r.Host+`/v2/library/alpine/blobs/sha256:abc`)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream, nil)
	req := httptest.NewRequest(http.MethodGet, "https://192.168.44.100/v2/library/alpine/manifests/latest", nil)
	req.Host = "192.168.44.100"
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(seenHost, strings.TrimPrefix(upstream.URL, "https://")) {
		t.Fatalf("upstream host was not used: %q", seenHost)
	}
	bodyAndHeaders := rec.Body.String() + rec.Header().Get("WWW-Authenticate") + rec.Header().Get("Location")
	if strings.Contains(bodyAndHeaders, strings.TrimPrefix(upstream.URL, "https://")) {
		t.Fatalf("response leaked upstream host: %s", bodyAndHeaders)
	}
	if got := rec.Header().Get("Location"); !strings.Contains(got, "192.168.44.100") {
		t.Fatalf("location was not rewritten: %q", got)
	}
}

func TestProxyReturnsOKForDockerMirrorPing(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called for local mirror ping")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream, nil)
	req := httptest.NewRequest(http.MethodGet, "https://192.168.44.100/v2/", nil)
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Docker-Distribution-API-Version"); got != "registry/2.0" {
		t.Fatalf("api version header = %q", got)
	}
}

func TestProxyForwardsTokenEndpoint(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"token":"abc"}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream, nil)
	req := httptest.NewRequest(http.MethodGet, "https://192.168.44.100/token?service=registry.docker.io", nil)
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != `{"token":"abc"}` {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestProxyFollowsRedirectInternally(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/library/alpine/blobs/sha256:abc" {
			http.Redirect(w, r, "/final/blob", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/final/blob" {
			_, _ = w.Write([]byte("blob-data"))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream, nil)
	req := httptest.NewRequest(http.MethodGet, "https://192.168.44.100/v2/library/alpine/blobs/sha256:abc", nil)
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "blob-data" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("Location") != "" {
		t.Fatalf("redirect leaked location: %q", rec.Header().Get("Location"))
	}
}

func TestProxyRejectsWriteMethods(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream should not be called")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream, nil)
	req := httptest.NewRequest(http.MethodPut, "https://192.168.44.100/v2/library/alpine/manifests/latest", nil)
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestProxyDiskCacheBlob(t *testing.T) {
	var hits int
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Docker-Content-Digest", "sha256:abc")
		_, _ = w.Write([]byte("cached-blob"))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream, func(cfg *config.Config) {
		cfg.EnableDiskCache = true
		cfg.DiskCacheDir = t.TempDir()
	})
	req := httptest.NewRequest(http.MethodGet, "https://192.168.44.100/v2/library/alpine/blobs/sha256:abc", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "https://192.168.44.100/v2/library/alpine/blobs/sha256:abc", nil)
	p.ServeHTTP(rec, req)

	if hits != 1 {
		t.Fatalf("upstream hits = %d", hits)
	}
	if rec.Header().Get("X-Registry-Mirror-Cache") != "HIT" {
		t.Fatalf("cache header = %q", rec.Header().Get("X-Registry-Mirror-Cache"))
	}
	if rec.Body.String() != "cached-blob" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestClientCIDRAllowList(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream, func(cfg *config.Config) {
		cfg.AllowedClientCIDRs = []string{"10.0.0.0/8"}
	})
	req := httptest.NewRequest(http.MethodGet, "https://192.168.44.100/v2/", nil)
	req.RemoteAddr = "192.168.1.20:12345"
	rec := httptest.NewRecorder()

	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestProxyUsesTLS12Minimum(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	p := newTestProxy(t, upstream, nil)
	tr, ok := p.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", p.client.Transport)
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.MinVersion != 0 && tr.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.Fatal("TLS minimum version is lower than 1.2")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
