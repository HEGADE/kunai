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
	"strconv"

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
	flag.StringVar(&cfg.DefaultEffort, "effort", os.Getenv("KUNAI_EFFORT"), "default reasoning effort for new sessions: low|medium|high|xhigh|max (optional)")
	flag.StringVar(&cfg.PublicURL, "public-url", os.Getenv("KUNAI_PUBLIC_URL"), "this machine's own tailnet origin (e.g. https://host.tailnet.ts.net:8443)")
	flag.StringVar(&cfg.HubURL, "hub-url", os.Getenv("KUNAI_HUB_URL"), "hub origin to forward Web Push wake-ups to (set on peer machines)")
	dataDir := flag.String("data", envOr("KUNAI_DATA", defaultDataDir()), "directory for VAPID keys, subscriptions, uploads")
	pushEmail := flag.String("push-email", os.Getenv("KUNAI_PUSH_EMAIL"), "VAPID contact (mailto) for Web Push")
	flag.BoolVar(&cfg.ThermalGuard, "thermal-guard", envBool("KUNAI_THERMAL_GUARD", false), "enable the thermal safety guard by default (stops all sessions when the host overheats)")
	flag.Float64Var(&cfg.ThermalSoftC, "thermal-soft-c", envFloat("KUNAI_THERMAL_SOFT_C", 90), "trip temperature in Celsius (Linux; 0 disables the temperature check)")
	flag.Float64Var(&cfg.ThermalMaxHours, "thermal-max-hours", envFloat("KUNAI_THERMAL_MAX_HOURS", 0), "stop unattended work after this many hours awake (0 = no cap)")
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

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
