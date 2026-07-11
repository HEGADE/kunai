package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/hegade/kunai/internal/claude"
)

// Manager owns all live sessions. It is safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// CreateOptions configure a new session.
type CreateOptions struct {
	Cwd   string
	Title string
	Model string
	// Resume, when set, reattaches to an existing CLI session id (loading its
	// transcript) rather than starting a fresh conversation.
	Resume string
	// Seed pre-populates the replay buffer with past turns (used with Resume so
	// the client sees the prior conversation).
	Seed []SeedTurn
}

// Create registers a new claude session and returns immediately; the CLI boots
// in the background so opening a session is instant. Boot failures surface to
// attached clients as an error event. Our handle id doubles as the CLI
// --session-id so --resume works with the same id after a restart.
func (m *Manager) Create(ctx context.Context, opts CreateOptions) (*Session, error) {
	if opts.Cwd == "" {
		return nil, errors.New("cwd required")
	}
	if fi, err := os.Stat(opts.Cwd); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("not a directory: %s", opts.Cwd)
	}
	id := opts.Resume
	if id == "" {
		id = newUUID()
	}

	m.mu.Lock()
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %s already live", id)
	}
	m.mu.Unlock()

	drvOpts := claude.Options{Cwd: opts.Cwd, Model: opts.Model}
	if opts.Resume != "" {
		drvOpts.Resume = opts.Resume
	} else {
		drvOpts.SessionID = id
	}
	drv := claude.NewSession(drvOpts)

	s := newSession(id, opts.Cwd, opts.Title, drv)
	s.model = opts.Model
	if len(opts.Seed) > 0 {
		s.Seed(opts.Seed)
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	// Reap from the registry when the session ends.
	go func() {
		<-s.Done()
		m.remove(id)
	}()

	// Boot the CLI off the request path. Prompts sent meanwhile queue in the
	// driver and flush once the process is up.
	go func() {
		if err := drv.Start(context.Background()); err != nil {
			s.FailStart(err.Error())
		}
	}()
	return s, nil
}

func (m *Manager) Get(id string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *Manager) List() []Meta {
	m.mu.Lock()
	metas := make([]Meta, 0, len(m.sessions))
	for _, s := range m.sessions {
		metas = append(metas, s.Meta())
	}
	m.mu.Unlock()
	sort.Slice(metas, func(i, j int) bool { return metas[i].CreatedAt.After(metas[j].CreatedAt) })
	return metas
}

func (m *Manager) Close(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	m.mu.Unlock()
	if ok {
		s.Close()
	}
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	all := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.Close()
	}
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// newUUID returns a random RFC 4122 v4 UUID (required by claude --session-id).
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
