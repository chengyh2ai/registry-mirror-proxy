package proxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"registry-mirror/internal/auth"
	"registry-mirror/internal/config"
)

type Proxy struct {
	upstreams      []*url.URL
	allowMethods   map[string]bool
	hideErrors     bool
	maxRedirects   int
	trustedHosts   map[string]bool
	client         *http.Client
	log            *slog.Logger
	metrics        *Metrics
	cache          *DiskCache
	allowedClients []*net.IPNet
	sem            chan struct{}
	upstreamUser   string
	upstreamPass   string
	credProvider   credentialProvider
}

type credentialProvider interface {
	BasicAuth(context.Context) (string, string, error)
}

func New(cfg config.Config, logger *slog.Logger) (*Proxy, error) {
	upstreams := make([]*url.URL, 0, len(cfg.Upstreams))
	trusted := make(map[string]bool)
	for _, raw := range cfg.Upstreams {
		u, err := url.Parse(raw)
		if err != nil {
			return nil, err
		}
		upstreams = append(upstreams, u)
		trusted[u.Hostname()] = true
	}
	for _, host := range cfg.TrustedRedirectHost {
		trusted[strings.ToLower(host)] = true
	}

	allowed := make(map[string]bool, len(cfg.AllowMethods))
	for _, method := range cfg.AllowMethods {
		allowed[strings.ToUpper(method)] = true
	}
	cidrs := make([]*net.IPNet, 0, len(cfg.AllowedClientCIDRs))
	for _, raw := range cfg.AllowedClientCIDRs {
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, err
		}
		cidrs = append(cidrs, n)
	}

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: cfg.UpstreamTimeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConns,
		IdleConnTimeout:       cfg.IdleTimeout,
		ResponseHeaderTimeout: cfg.UpstreamTimeout,
		TLSHandshakeTimeout:   cfg.UpstreamTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:     true,
	}
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= cfg.MaxRedirects {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" {
				return errors.New("refuse non-https redirect")
			}
			return nil
		},
	}

	var cache *DiskCache
	var err error
	if cfg.EnableDiskCache {
		cache, err = NewDiskCache(cfg.DiskCacheDir, cfg.DiskCacheMaxBytes, cfg.Upstreams)
		if err != nil {
			return nil, err
		}
	}
	var sem chan struct{}
	if cfg.MaxConcurrentRequests > 0 {
		sem = make(chan struct{}, cfg.MaxConcurrentRequests)
	}
	var provider credentialProvider
	if cfg.VolcAuthEnabled {
		provider, err = auth.NewVolcProvider(auth.VolcConfig{
			AccessKey:     cfg.VolcAccessKey,
			SecretKey:     cfg.VolcSecretKey,
			Region:        cfg.VolcRegion,
			Endpoint:      cfg.VolcEndpoint,
			Registry:      cfg.VolcRegistry,
			RefreshBefore: cfg.VolcRefreshBefore,
		})
		if err != nil {
			return nil, err
		}
	}

	return &Proxy{
		upstreams:      upstreams,
		allowMethods:   allowed,
		hideErrors:     cfg.HideUpstreamErrors,
		maxRedirects:   cfg.MaxRedirects,
		trustedHosts:   trusted,
		client:         client,
		log:            logger,
		metrics:        &Metrics{},
		cache:          cache,
		allowedClients: cidrs,
		sem:            sem,
		upstreamUser:   cfg.UpstreamUsername,
		upstreamPass:   cfg.UpstreamPassword,
		credProvider:   provider,
	}, nil
}

func (p *Proxy) Metrics() *Metrics {
	return p.metrics
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &responseRecorder{ResponseWriter: w}
	defer func() {
		p.metrics.Requests.Add(1)
		p.metrics.BytesSent.Add(uint64(max64(rec.bytes, 0)))
		p.log.Info("request",
			"method", r.Method,
			"path", r.URL.RequestURI(),
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"client_ip", clientIP(r),
		)
	}()

	if !p.clientAllowed(r) {
		writeRegistryError(rec, http.StatusForbidden, "DENIED", "client is not allowed")
		return
	}
	if !p.acquire() {
		writeRegistryError(rec, http.StatusTooManyRequests, "TOOMANYREQUESTS", "too many concurrent requests")
		return
	}
	defer p.release()
	if !p.allowMethods[r.Method] {
		rec.Header().Set("Allow", strings.Join(p.allowedMethodList(), ", "))
		writeRegistryError(rec, http.StatusMethodNotAllowed, "UNSUPPORTED", "method is not allowed")
		return
	}
	if r.URL.Path == "/healthz" {
		writeJSON(rec, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		rec.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		rec.WriteHeader(http.StatusOK)
		return
	}
	if !proxyablePath(r.URL.Path) {
		writeRegistryError(rec, http.StatusNotFound, "NAME_UNKNOWN", "unknown route")
		return
	}
	if p.cache != nil && p.cache.Serve(rec, r) {
		p.metrics.CacheHits.Add(1)
		return
	}
	if p.cache != nil && cacheable(r) {
		p.metrics.CacheMisses.Add(1)
	}

	resp, err := p.doUpstream(r)
	if err != nil {
		p.metrics.UpstreamFails.Add(1)
		p.log.Warn("upstream request failed", "error", err)
		writeRegistryError(rec, http.StatusBadGateway, "UNAVAILABLE", "registry mirror upstream unavailable")
		return
	}
	defer resp.Body.Close()

	p.writeUpstreamResponse(rec, r, resp)
}

func (p *Proxy) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.upstreams[0].String()+"/v2/", nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("upstream returned %s", resp.Status)
	}
	return nil
}

func (p *Proxy) doUpstream(r *http.Request) (*http.Response, error) {
	var lastErr error
	for _, upstream := range p.upstreams {
		resp, err := p.doOneUpstream(r, upstream, "")
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusUnauthorized && r.Header.Get("Authorization") == "" {
			_ = resp.Body.Close()
			token, tokenErr := p.fetchBearerToken(r, upstream, resp)
			if tokenErr == nil && token != "" {
				resp, err = p.doOneUpstream(r, upstream, "Bearer "+token)
			} else {
				err = tokenErr
			}
		}
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 && len(p.upstreams) > 1 {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("upstream %s returned %s", upstream.Host, resp.Status)
			continue
		}
		return resp, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no upstream available")
	}
	return nil, lastErr
}

func (p *Proxy) doOneUpstream(r *http.Request, upstream *url.URL, authorization string) (*http.Response, error) {
	req, err := p.newUpstreamRequest(r, upstream)
	if err != nil {
		return nil, err
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	} else {
		if err := p.setUpstreamBasicAuth(req); err != nil {
			return nil, err
		}
	}
	return p.client.Do(req)
}

func (p *Proxy) newUpstreamRequest(r *http.Request, upstream *url.URL) (*http.Request, error) {
	target := *upstream
	target.Path = singleJoiningSlash(upstream.Path, r.URL.Path)
	target.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Host = upstream.Host
	copyProxyRequestHeaders(req.Header, r.Header)
	req.Header.Set("Host", upstream.Host)
	req.Header.Set("X-Forwarded-Host", r.Host)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-For", appendForwardedFor(r))
	return req, nil
}

type bearerChallenge struct {
	Realm   string
	Service string
	Scope   string
}

func (p *Proxy) fetchBearerToken(r *http.Request, upstream *url.URL, resp *http.Response) (string, error) {
	challenge, err := parseBearerChallenge(resp.Header.Values("WWW-Authenticate"))
	if err != nil {
		return "", err
	}
	realm, err := url.Parse(challenge.Realm)
	if err != nil {
		return "", err
	}
	if !realm.IsAbs() {
		realm.Scheme = upstream.Scheme
		realm.Host = upstream.Host
	}
	if realm.Scheme != "https" {
		return "", errors.New("refuse non-https token realm")
	}
	q := realm.Query()
	if challenge.Service != "" && q.Get("service") == "" {
		q.Set("service", challenge.Service)
	}
	if challenge.Scope != "" && q.Get("scope") == "" {
		q.Set("scope", challenge.Scope)
	}
	realm.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	copyProxyRequestHeaders(req.Header, r.Header)
	req.Header.Del("Authorization")
	if err := p.setUpstreamBasicAuth(req); err != nil {
		return "", err
	}
	req.Host = realm.Host
	if cookies := cookiesFromSetCookie(resp.Header.Values("Set-Cookie")); cookies != "" {
		req.Header.Set("Cookie", cookies)
	}
	tokenResp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode < 200 || tokenResp.StatusCode >= 300 {
		return "", fmt.Errorf("token endpoint returned %s", tokenResp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokenResp.Body, 4<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", errors.New("token endpoint returned no token")
}

func (p *Proxy) setUpstreamBasicAuth(req *http.Request) error {
	if p.credProvider != nil {
		username, password, err := p.credProvider.BasicAuth(req.Context())
		if err != nil {
			return err
		}
		req.SetBasicAuth(username, password)
		return nil
	}
	if p.upstreamUser == "" && p.upstreamPass == "" {
		return nil
	}
	req.SetBasicAuth(p.upstreamUser, p.upstreamPass)
	return nil
}

func parseBearerChallenge(values []string) (bearerChallenge, error) {
	for _, value := range values {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "bearer ") {
			continue
		}
		params := parseAuthParams(strings.TrimSpace(value[len("Bearer "):]))
		realm := params["realm"]
		if realm == "" {
			return bearerChallenge{}, errors.New("bearer challenge missing realm")
		}
		return bearerChallenge{Realm: realm, Service: params["service"], Scope: params["scope"]}, nil
	}
	return bearerChallenge{}, errors.New("bearer challenge not found")
}

func parseAuthParams(raw string) map[string]string {
	out := make(map[string]string)
	for _, part := range splitAuthParams(raw) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		out[key] = value
	}
	return out
}

func splitAuthParams(raw string) []string {
	var parts []string
	var b strings.Builder
	inQuote := false
	for _, r := range raw {
		switch r {
		case '"':
			inQuote = !inQuote
			b.WriteRune(r)
		case ',':
			if inQuote {
				b.WriteRune(r)
				continue
			}
			parts = append(parts, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		parts = append(parts, b.String())
	}
	return parts
}

func cookiesFromSetCookie(values []string) string {
	var cookies []string
	for _, value := range values {
		nameValue, _, _ := strings.Cut(value, ";")
		nameValue = strings.TrimSpace(nameValue)
		if nameValue != "" {
			cookies = append(cookies, nameValue)
		}
	}
	return strings.Join(cookies, "; ")
}

func (p *Proxy) writeUpstreamResponse(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	sanitizeResponseHeaders(resp.Header, p.upstreams, publicBaseURL(r))
	copyHeader(w.Header(), resp.Header)
	w.Header().Set("X-Registry-Mirror-Proxy", "registry-mirror-proxy")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	writer, commit, abort := p.cache.Store(r, resp, w)
	n, err := io.Copy(writer, resp.Body)
	if err != nil {
		abort()
		p.log.Warn("stream response failed", "error", err)
		return
	}
	commit(n)
}

func (p *Proxy) clientAllowed(r *http.Request) bool {
	if len(p.allowedClients) == 0 {
		return true
	}
	ip := net.ParseIP(clientIP(r))
	if ip == nil {
		return false
	}
	for _, cidr := range p.allowedClients {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

func (p *Proxy) acquire() bool {
	if p.sem == nil {
		return true
	}
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *Proxy) release() {
	if p.sem == nil {
		return
	}
	<-p.sem
}

func proxyablePath(path string) bool {
	return strings.HasPrefix(path, "/v2/") ||
		strings.HasPrefix(path, "/token") ||
		strings.HasPrefix(path, "/service/token") ||
		strings.HasPrefix(path, "/oauth2/token") ||
		strings.Contains(strings.ToLower(path), "token")
}

func (p *Proxy) allowedMethodList() []string {
	methods := make([]string, 0, len(p.allowMethods))
	for method := range p.allowMethods {
		methods = append(methods, method)
	}
	return methods
}

func copyProxyRequestHeaders(dst, src http.Header) {
	for k, values := range src {
		if hopByHopHeader(k) {
			continue
		}
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

func copyHeader(dst, src http.Header) {
	for k, values := range src {
		if hopByHopHeader(k) {
			continue
		}
		dst.Del(k)
		for _, v := range values {
			dst.Add(k, v)
		}
	}
}

func sanitizeResponseHeaders(h http.Header, upstreams []*url.URL, publicBase string) {
	rewriteAuthenticateRealm(h, publicBase)
	for _, upstream := range upstreams {
		host := upstream.Host
		hostname := upstream.Hostname()
		for key, values := range h {
			for i, value := range values {
				value = strings.ReplaceAll(value, host, hostFromBase(publicBase))
				value = strings.ReplaceAll(value, hostname, hostFromBase(publicBase))
				h[key][i] = value
			}
		}
	}
	if location := h.Get("Location"); location != "" {
		if u, err := url.Parse(location); err == nil && u.IsAbs() {
			u.Scheme = "https"
			u.Host = hostFromBase(publicBase)
			h.Set("Location", u.String())
		}
	}
}

var quotedRealmRE = regexp.MustCompile(`realm="https://[^"]+"`)
var bareRealmRE = regexp.MustCompile(`realm=https://[^,\s]+`)

func rewriteAuthenticateRealm(h http.Header, publicBase string) {
	values := h.Values("WWW-Authenticate")
	if len(values) == 0 {
		return
	}
	h.Del("WWW-Authenticate")
	for _, value := range values {
		value = quotedRealmRE.ReplaceAllStringFunc(value, func(match string) string {
			raw := strings.TrimSuffix(strings.TrimPrefix(match, `realm="`), `"`)
			return `realm="` + rewriteRealmURL(raw, publicBase) + `"`
		})
		value = bareRealmRE.ReplaceAllStringFunc(value, func(match string) string {
			raw := strings.TrimPrefix(match, "realm=")
			return "realm=" + rewriteRealmURL(raw, publicBase)
		})
		h.Add("WWW-Authenticate", value)
	}
}

func rewriteRealmURL(raw, publicBase string) string {
	realm, err := url.Parse(raw)
	if err != nil || !realm.IsAbs() {
		return raw
	}
	pub, err := url.Parse(publicBase)
	if err != nil || pub.Host == "" {
		return raw
	}
	realm.Scheme = "https"
	realm.Host = pub.Host
	return realm.String()
}

func hopByHopHeader(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate",
		"proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func writeRegistryError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"errors": []map[string]string{{"code": code, "message": message}},
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func publicBaseURL(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	return "https://" + host
}

func hostFromBase(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return u.Host
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}

func appendForwardedFor(r *http.Request) string {
	ip := clientIP(r)
	if prior := r.Header.Get("X-Forwarded-For"); prior != "" {
		return prior + ", " + ip
	}
	return ip
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
