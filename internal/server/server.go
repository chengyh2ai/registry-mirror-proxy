package server

import (
	"context"
	"net/http"
	"time"

	"registry-mirror/internal/config"
	"registry-mirror/internal/proxy"
)

func New(cfg config.Config, p *proxy.Proxy) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
	})
	if cfg.EnableReadyCheck {
		mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			if err := p.Ready(ctx); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"unavailable"}` + "\n"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ready"}` + "\n"))
		})
	}
	if cfg.EnableMetrics {
		mux.Handle("/metrics", p.Metrics())
	}
	mux.Handle("/token", p)
	mux.Handle("/token/", p)
	mux.Handle("/service/token", p)
	mux.Handle("/service/token/", p)
	mux.Handle("/oauth2/token", p)
	mux.Handle("/oauth2/token/", p)
	mux.Handle("/v2/", p)
	mux.Handle("/v2", p)

	return &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}
}
