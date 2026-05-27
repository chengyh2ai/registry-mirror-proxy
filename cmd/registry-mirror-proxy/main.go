package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"registry-mirror/internal/config"
	"registry-mirror/internal/logging"
	"registry-mirror/internal/proxy"
	"registry-mirror/internal/server"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("load config failed", "error", err)
		os.Exit(1)
	}
	logger := logging.New(cfg.LogLevel)
	p, err := proxy.New(cfg, logger)
	if err != nil {
		logger.Error("initialize proxy failed", "error", err)
		os.Exit(1)
	}

	srv := server.New(cfg, p)
	logger.Info("registry mirror proxy starting", "addr", cfg.ListenAddr, "upstreams", cfg.Upstreams)
	if cfg.TLSCertFile != "" {
		err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	} else {
		logger.Warn("TLS is disabled; Docker clients using https:// will not connect to this listener")
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
