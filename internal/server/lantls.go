package server

// A certificate for the network listener.
//
// It is self-signed, so the browser shows a warning the first time and you accept
// it once per device. That is worth being clear about, because the warning looks
// like the scary thing here and it is the opposite: WITHOUT a certificate the PIN
// and every request after it cross the network in the clear, which on a shared
// wifi means anyone capturing packets has both. Encryption is what makes the PIN
// worth having at all; the warning is only the browser saying it cannot vouch for
// who we are, which on your own machine you already know.
//
// The key never leaves the machine and the certificate is kept, not regenerated,
// so the exception you accept on a device keeps working across restarts. A cert
// that changed on every boot would retrain you to click through warnings, which
// is the habit this is trying not to build.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// lanCertLife is deliberately long. This certificate identifies a machine on your
// own network to you, and an expiry only creates a day when the tablet stops
// working for a reason nobody remembers.
const lanCertLife = 5 * 365 * 24 * time.Hour

// lanTLS loads the LAN certificate, creating one covering hosts if it is missing
// or no longer covers them (a new network, a changed address).
func lanTLS(dataDir string, hosts []string) (*tls.Config, error) {
	dir := filepath.Join(dataDir, "lan-tls")
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")

	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && certCovers(cert, hosts) {
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPEM, keyPEM, err := newSelfSigned(hosts)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// certCovers reports whether the stored certificate still names every address we
// are about to serve, and has not expired.
func certCovers(cert tls.Certificate, hosts []string) bool {
	if len(cert.Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil || time.Now().After(leaf.NotAfter) {
		return false
	}
	for _, h := range hosts {
		if leaf.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

// newSelfSigned mints a certificate naming hosts, plus loopback so the same
// listener can be reached locally without a second one.
func newSelfSigned(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "kunai on this network", Organization: []string{"kunai"}},
		NotBefore:    time.Now().Add(-time.Hour), // tolerate a skewed clock on the client
		NotAfter:     time.Now().Add(lanCertLife),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// Not a CA. It signs nothing but itself, so accepting it trusts one machine
		// on your network and not a new authority that could vouch for any site.
		BasicConstraintsValid: true,
	}
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.IPv4(127, 0, 0, 1), net.IPv6loopback)
	tmpl.DNSNames = append(tmpl.DNSNames, "localhost")
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}
