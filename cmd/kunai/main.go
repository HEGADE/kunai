// Command kunai is the self-hosted, relay-free server that wraps Claude Code and
// serves a mobile PWA over Tailscale. It binds to a tailnet address, drives one
// `claude` process per session over stdio, and bridges each to the phone over a
// WebSocket, with detached-reconnect resume and (later) Web Push.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/hegade/kunai/internal/push"
	"github.com/hegade/kunai/internal/server"
	"github.com/hegade/kunai/internal/session"
)

func main() {
	cfg := server.Config{}
	flag.StringVar(&cfg.Addr, "addr", envOr("KUNAI_ADDR", "127.0.0.1:8443"), "bind address (use the tailnet IP in production)")
	flag.StringVar(&cfg.TLSCert, "tls-cert", os.Getenv("KUNAI_TLS_CERT"), "TLS certificate (tailscale cert); empty = plain HTTP (dev only)")
	flag.StringVar(&cfg.TLSKey, "tls-key", os.Getenv("KUNAI_TLS_KEY"), "TLS key (tailscale cert)")
	flag.StringVar(&cfg.DefaultModel, "model", os.Getenv("KUNAI_MODEL"), "default model for new sessions (optional)")
	flag.StringVar(&cfg.PublicURL, "public-url", os.Getenv("KUNAI_PUBLIC_URL"), "this machine's own tailnet origin (e.g. https://host.tailnet.ts.net:8443)")
	flag.StringVar(&cfg.HubURL, "hub-url", os.Getenv("KUNAI_HUB_URL"), "hub origin to forward Web Push wake-ups to (set on peer machines)")
	dataDir := flag.String("data", envOr("KUNAI_DATA", defaultDataDir()), "directory for VAPID keys, subscriptions, uploads")
	pushEmail := flag.String("push-email", os.Getenv("KUNAI_PUSH_EMAIL"), "VAPID contact (mailto) for Web Push")
	flag.Parse()
	cfg.DataDir = *dataDir

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	mgr := session.NewManager()
	defer mgr.CloseAll()

	srv := server.New(cfg, mgr)

	subscriber := ""
	if *pushEmail != "" {
		subscriber = "mailto:" + *pushEmail
	}
	if pm, err := push.New(filepath.Join(*dataDir, "push"), subscriber); err != nil {
		log.Printf("web push disabled: %v", err)
	} else {
		srv.SetPush(pm)
	}

	if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("server: %v", err)
	}
}

func defaultDataDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".kunai")
	}
	return ".kunai"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
