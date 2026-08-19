package ghapp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pemFor(t *testing.T, typ string, der []byte) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// GitHub hands out PKCS#1, but a key round-tripped through openssl or a secrets
// manager often comes back as PKCS#8. Refusing that would be an inscrutable
// failure for someone who did nothing wrong.
func TestLoadCredentialsAcceptsBothPEMEncodings(t *testing.T) {
	key := testKey(t)

	pkcs1 := pemFor(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	if _, err := LoadCredentials("123", pkcs1); err != nil {
		t.Fatalf("PKCS#1 rejected: %v", err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials("123", pemFor(t, "PRIVATE KEY", der)); err != nil {
		t.Fatalf("PKCS#8 rejected: %v", err)
	}
}

// Every failure has to describe the SHAPE of what was found and never its
// contents, because the contents are the credential.
func TestLoadCredentialsFailuresNeverQuoteTheKey(t *testing.T) {
	secret := "SUPER-SECRET-KEY-MATERIAL"
	notPEM := []byte("-----BEGIN NONSENSE-----\n" + secret + "\n")

	if _, err := LoadCredentials("123", notPEM); err == nil {
		t.Fatal("a non-PEM key was accepted")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("the error quotes the key material: %v", err)
	}

	// A valid PEM block holding the wrong kind of key is named as such, so nobody
	// hunts through a file that is perfectly fine and simply not RSA.
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	_, err = LoadCredentials("123", pemFor(t, "PRIVATE KEY", der))
	if err == nil || !strings.Contains(err.Error(), "RSA") {
		t.Fatalf("an ECDSA key should be refused by name, got %v", err)
	}
}

// "Not set up yet" is an ordinary state, distinguishable with errors.Is, so the
// UI can say "add a key" instead of showing a failure.
func TestMissingCredentialsAreDistinguishable(t *testing.T) {
	if _, err := LoadCredentials("", []byte("x")); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("a missing App id should be ErrNoCredentials, got %v", err)
	}
	if _, err := LoadCredentials("123", nil); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("a missing key should be ErrNoCredentials, got %v", err)
	}
	missing := filepath.Join(t.TempDir(), "nope.pem")
	if _, err := LoadCredentialsFile("123", missing); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("a missing key file should be ErrNoCredentials, got %v", err)
	}
}

func TestLoadCredentialsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pemFor(t, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(testKey(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := LoadCredentialsFile(" 123456 ", path)
	if err != nil {
		t.Fatal(err)
	}
	if creds.AppID != "123456" {
		t.Errorf("AppID = %q, want it trimmed", creds.AppID)
	}
}
