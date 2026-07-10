// Package push sends Web Push wake-ups over VAPID. The payload is always
// generic — "a session needs you" — never session content: the real state is
// pulled fresh over Tailscale when the phone reconnects. Apple's push service is
// the one hop not on the tailnet, and it only ever carries this opaque nudge.
package push

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Manager holds the VAPID keypair and the set of browser subscriptions, both
// persisted so they survive restarts.
type Manager struct {
	dir        string
	subscriber string // VAPID "mailto:" contact

	mu      sync.Mutex
	pubKey  string
	privKey string
	subs    map[string]*webpush.Subscription // keyed by endpoint
}

type keyfile struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// New loads (or generates) the VAPID keys and loads saved subscriptions from dir.
func New(dir, subscriber string) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if subscriber == "" {
		subscriber = "mailto:kunai@localhost"
	}
	m := &Manager{dir: dir, subscriber: subscriber, subs: map[string]*webpush.Subscription{}}

	if err := m.loadKeys(); err != nil {
		return nil, err
	}
	m.loadSubs()
	return m, nil
}

func (m *Manager) loadKeys() error {
	path := filepath.Join(m.dir, "vapid.json")
	if b, err := os.ReadFile(path); err == nil {
		var kf keyfile
		if json.Unmarshal(b, &kf) == nil && kf.Public != "" {
			m.pubKey, m.privKey = kf.Public, kf.Private
			return nil
		}
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return err
	}
	m.pubKey, m.privKey = pub, priv
	b, _ := json.Marshal(keyfile{Public: pub, Private: priv})
	return os.WriteFile(path, b, 0o600)
}

func (m *Manager) loadSubs() {
	b, err := os.ReadFile(filepath.Join(m.dir, "subscriptions.json"))
	if err != nil {
		return
	}
	var list []*webpush.Subscription
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for _, s := range list {
		if s.Endpoint != "" {
			m.subs[s.Endpoint] = s
		}
	}
}

func (m *Manager) saveSubsLocked() {
	list := make([]*webpush.Subscription, 0, len(m.subs))
	for _, s := range m.subs {
		list = append(list, s)
	}
	b, _ := json.Marshal(list)
	_ = os.WriteFile(filepath.Join(m.dir, "subscriptions.json"), b, 0o600)
}

// PublicKey returns the VAPID application server key the browser subscribes with.
func (m *Manager) PublicKey() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pubKey
}

// Subscribe records a browser push subscription.
func (m *Manager) Subscribe(sub *webpush.Subscription) {
	if sub == nil || sub.Endpoint == "" {
		return
	}
	m.mu.Lock()
	m.subs[sub.Endpoint] = sub
	m.saveSubsLocked()
	m.mu.Unlock()
}

// Enabled reports whether any device is subscribed.
func (m *Manager) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.subs) > 0
}

// Notify sends a generic wake-up to every subscribed device. Subscriptions the
// push service reports as gone (404/410) are pruned.
func (m *Manager) Notify(title, body string) {
	m.mu.Lock()
	subs := make([]*webpush.Subscription, 0, len(m.subs))
	for _, s := range m.subs {
		subs = append(subs, s)
	}
	pub, priv, subscriber := m.pubKey, m.privKey, m.subscriber
	m.mu.Unlock()

	payload, _ := json.Marshal(map[string]string{"title": title, "body": body})
	var dead []string
	for _, s := range subs {
		resp, err := webpush.SendNotification(payload, s, &webpush.Options{
			Subscriber:      subscriber,
			VAPIDPublicKey:  pub,
			VAPIDPrivateKey: priv,
			TTL:             30,
			Urgency:         webpush.UrgencyHigh,
		})
		if err != nil {
			log.Printf("push: send error: %v", err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
			dead = append(dead, s.Endpoint)
		}
	}
	if len(dead) > 0 {
		m.mu.Lock()
		for _, e := range dead {
			delete(m.subs, e)
		}
		m.saveSubsLocked()
		m.mu.Unlock()
	}
}
