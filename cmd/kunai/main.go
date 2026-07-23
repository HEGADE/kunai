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
	"strings"

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
	flag.Float64Var(&cfg.ThermalHardC, "thermal-hard-c", envFloat("KUNAI_THERMAL_HARD_C", 0), "power-off ceiling in Celsius, used only with -thermal-action=poweroff (0 = never)")
	flag.StringVar(&cfg.ThermalAction, "thermal-action", envOr("KUNAI_THERMAL_ACTION", "sleep"), "what a hard trip does: sleep (stop and cool) or poweroff (needs the install-time privilege)")
	tgToken := flag.String("telegram-token", os.Getenv("KUNAI_TELEGRAM_TOKEN"), "Telegram bot token (empty disables the bot)")
	tgAllowed := flag.String("telegram-allow", os.Getenv("KUNAI_TELEGRAM_ALLOW"), "comma-separated Telegram user ids allowed to drive kunai")
	flag.BoolVar(&cfg.TelegramDetail, "telegram-detail", envBool("KUNAI_TELEGRAM_DETAIL", false), "send tool inputs and outputs to Telegram (file contents and command output leave the machine)")
	flag.BoolVar(&cfg.NativeCodex, "native-codex", envBool("KUNAI_NATIVE_CODEX", true), "route a Codex provider through kunai's own in-process proxy instead of the CLIProxyAPI sidecar (default on; falls back to the sidecar if the Codex login is missing)")
	flag.BoolVar(&cfg.NativeGrok, "native-grok", envBool("KUNAI_NATIVE_GROK", true), "route a Grok provider through kunai's own in-process proxy, reading the grok CLI login (default on; falls back to the sidecar if the login is missing)")
	flag.Parse()
	cfg.DataDir = *dataDir
	cfg.TelegramToken = *tgToken
	cfg.TelegramAllowed = parseIDs(*tgAllowed)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	mgr := session.NewManager()
	defer mgr.CloseAll()

	srv := server.New(cfg, mgr)

	// The VAPID "subscriber" is the contact in the signed push token. Apple's Web
	// Push rejects a mailto: subscriber with 403 (so iPhones never got a single
	// notification) and requires an https: one, while Chrome/FCM accepts anything
	// — which is why desktop worked and iOS silently did not. The machine's own
	// public URL is a valid https contact and is exactly what an iOS-capable hub
	// already has; the mailto is only a fallback for a dev box with no public URL,
	// where iOS push cannot work anyway but FCM still can.
	subscriber := cfg.PublicURL
	if subscriber == "" && *pushEmail != "" {
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

// parseIDs reads the comma-separated Telegram user ids from a flag. Anything
// unparseable is dropped rather than guessed at: an id that does not survive
// this becomes a user who cannot use the bot, which is the safe direction.
func parseIDs(s string) []int64 {
	var out []int64
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if id, err := strconv.ParseInt(f, 10, 64); err == nil {
			out = append(out, id)
		} else {
			log.Printf("telegram: ignoring unreadable user id %q", f)
		}
	}
	return out
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
