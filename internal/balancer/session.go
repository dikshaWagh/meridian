package balancer

import (
	"sync"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

const (
	MaxSessions = 200
	SessionTTL  = 30 * time.Minute
)

type Session struct {
	ID       string
	Model    string
	WorkerID string
	Messages []api.Message
	LastUsed time.Time
	inFlight bool
	mu       sync.Mutex
}

type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	maxCap   int
	ttl      time.Duration
}

func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
		maxCap:   MaxSessions,
		ttl:      SessionTTL,
	}
}

func (s *SessionStore) Get(id string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

func (s *SessionStore) Create(id, model, workerID string) *Session {
	sess, _ := s.CreateIfAbsent(id, model, workerID)
	return sess
}

// CreateIfAbsent atomically inserts a session unless one with the same ID
// already exists. The second return value reports whether this call created it.
func (s *SessionStore) CreateIfAbsent(id, model, workerID string) (*Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[id]; ok {
		return sess, false
	}

	if len(s.sessions) >= s.maxCap {
		var oldestID string
		var oldestTime time.Time
		first := true
		for k, sess := range s.sessions {
			sess.mu.Lock()
			busy := sess.inFlight
			lu := sess.LastUsed
			sess.mu.Unlock()
			if busy {
				continue
			}
			if first || lu.Before(oldestTime) {
				oldestID = k
				oldestTime = lu
				first = false
			}
		}
		if oldestID != "" {
			delete(s.sessions, oldestID)
		}
	}

	sess := &Session{
		ID:       id,
		Model:    model,
		WorkerID: workerID,
		LastUsed: time.Now(),
	}
	s.sessions[id] = sess
	return sess, true
}

func (s *SessionStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, sess := range s.sessions {
		sess.mu.Lock()
		busy := sess.inFlight
		lu := sess.LastUsed
		sess.mu.Unlock()
		if !busy && now.Sub(lu) > s.ttl {
			delete(s.sessions, id)
		}
	}
}

func (s *SessionStore) SessionCountPerWorker() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	counts := make(map[string]int)
	for _, sess := range s.sessions {
		sess.mu.Lock()
		wID := sess.WorkerID
		sess.mu.Unlock()
		counts[wID]++
	}
	return counts
}

func (s *SessionStore) TotalCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}
