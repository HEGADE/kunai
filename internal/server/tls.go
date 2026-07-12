package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TLS certificates minted with `tailscale cert` expire in ~90 days and are not
// auto-renewed by Tailscale. certKeeper closes that gap: it serves the keypair
// from disk (hot-reloading when the files change, so a re-mint needs no
// restart) and runs `tailscale cert` to renew the files before they expire.
type certKeeper struct {
	certFile string
	keyFile  string
	domain   string // tailnet FQDN to pass to `tailscale cert`

	mu       sync.Mutex
	cert     *tls.Certificate
	loadedAt time.Time // mtime of the cert file we last loaded
}

var errNoCert = errors.New("kunai: no TLS certificate loaded")

func newCertKeeper(certFile, keyFile, publicURL string) *certKeeper {
	return &certKeeper{
		certFile: certFile,
		keyFile:  keyFile,
		domain:   certDomain(publicURL, certFile),
	}
}

// certDomain is the FQDN to renew: the host of the public URL, or the cert file
// name without its extension as a fallback (tailscale writes <fqdn>.crt).
func certDomain(publicURL, certFile string) string {
	if publicURL != "" {
		if u, err := url.Parse(publicURL); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
	}
	base := filepath.Base(certFile)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// GetCertificate is the tls.Config hook. It returns the cached keypair, first
// reloading from disk if the cert file changed since the last load.
func (k *certKeeper) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if fi, err := os.Stat(k.certFile); err == nil && (k.cert == nil || fi.ModTime().After(k.loadedAt)) {
		if c, err := tls.LoadX509KeyPair(k.certFile, k.keyFile); err == nil {
			k.cert = &c
			k.loadedAt = fi.ModTime()
		} else if k.cert == nil {
			return nil, err
		}
	}
	if k.cert == nil {
		return nil, errNoCert
	}
	return k.cert, nil
}

// renewLoop checks the cert's expiry shortly after boot and then periodically,
// re-minting via `tailscale cert` when it is within renewBefore of expiring.
func (k *certKeeper) renewLoop(ctx context.Context) {
	const (
		checkEvery  = 12 * time.Hour
		renewBefore = 20 * 24 * time.Hour
	)
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			k.maybeRenew(renewBefore)
			t.Reset(checkEvery)
		}
	}
}

func (k *certKeeper) maybeRenew(before time.Duration) {
	expiry := k.leafExpiry()
	if expiry.IsZero() || time.Until(expiry) > before {
		return
	}
	bin := tailscaleBin()
	if bin == "" {
		log.Printf("kunai: TLS cert for %s expires %s but the tailscale CLI was not found; renew manually", k.domain, expiry.Format(time.RFC3339))
		return
	}
	log.Printf("kunai: renewing TLS cert for %s (expires %s)", k.domain, expiry.Format(time.RFC3339))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "cert", "--cert-file", k.certFile, "--key-file", k.keyFile, k.domain)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("kunai: tailscale cert renew failed: %v: %s", err, strings.TrimSpace(string(out)))
		return
	}
	// Force a reload on the next handshake.
	k.mu.Lock()
	k.loadedAt = time.Time{}
	k.mu.Unlock()
	log.Printf("kunai: TLS cert for %s renewed", k.domain)
}

// leafExpiry returns the NotAfter of the loaded leaf certificate (or reads it
// from disk if nothing is cached yet).
func (k *certKeeper) leafExpiry() time.Time {
	k.mu.Lock()
	cert := k.cert
	k.mu.Unlock()
	if cert == nil {
		c, err := tls.LoadX509KeyPair(k.certFile, k.keyFile)
		if err != nil {
			return time.Time{}
		}
		cert = &c
	}
	if len(cert.Certificate) == 0 {
		return time.Time{}
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return time.Time{}
	}
	return leaf.NotAfter
}
