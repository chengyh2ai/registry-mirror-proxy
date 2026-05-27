package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type DiskCache struct {
	dir      string
	maxByte  int64
	upstream []string
}

type cacheMeta struct {
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Size   int64       `json:"size"`
}

func NewDiskCache(dir string, maxBytes int64, upstream []string) (*DiskCache, error) {
	if dir == "" {
		return nil, errors.New("cache dir is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &DiskCache{dir: dir, maxByte: maxBytes, upstream: upstream}, nil
}

func cacheable(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		r.Header.Get("Range") == "" &&
		strings.Contains(r.URL.Path, "/blobs/sha256:")
}

func (c *DiskCache) key(r *http.Request) string {
	sum := sha256.Sum256([]byte(r.URL.Path + "?" + r.URL.RawQuery))
	return hex.EncodeToString(sum[:])
}

func (c *DiskCache) paths(key string) (string, string, string) {
	return filepath.Join(c.dir, key+".body"), filepath.Join(c.dir, key+".json"), filepath.Join(c.dir, key+".tmp")
}

func (c *DiskCache) Serve(w http.ResponseWriter, r *http.Request) bool {
	if c == nil || !cacheable(r) {
		return false
	}
	bodyPath, metaPath, _ := c.paths(c.key(r))
	body, err := os.Open(bodyPath)
	if err != nil {
		return false
	}
	defer body.Close()
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return false
	}
	var meta cacheMeta
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return false
	}
	copyHeader(w.Header(), meta.Header)
	w.Header().Set("X-Registry-Mirror-Cache", "HIT")
	w.WriteHeader(meta.Status)
	_, _ = io.Copy(w, body)
	return true
}

func (c *DiskCache) Store(r *http.Request, resp *http.Response, writer http.ResponseWriter) (io.Writer, func(int64), func()) {
	if c == nil || !cacheable(r) || resp.StatusCode != http.StatusOK {
		return writer, func(int64) {}, func() {}
	}
	key := c.key(r)
	bodyPath, metaPath, tmpPath := c.paths(key)
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return writer, func(int64) {}, func() {}
	}
	mw := io.MultiWriter(writer, tmp)
	commit := func(size int64) {
		defer tmp.Close()
		if c.maxByte > 0 && size > c.maxByte {
			_ = os.Remove(tmpPath)
			return
		}
		meta := cacheMeta{Status: resp.StatusCode, Header: cloneCacheHeader(resp.Header), Size: size}
		b, err := json.Marshal(meta)
		if err != nil {
			_ = os.Remove(tmpPath)
			return
		}
		if err := os.Rename(tmpPath, bodyPath); err != nil {
			_ = os.Remove(tmpPath)
			return
		}
		_ = os.WriteFile(metaPath, b, 0o644)
	}
	abort := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	return mw, commit, abort
}

func cloneCacheHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, values := range h {
		if strings.EqualFold(k, "Set-Cookie") {
			continue
		}
		out[k] = append([]string(nil), values...)
	}
	return out
}
