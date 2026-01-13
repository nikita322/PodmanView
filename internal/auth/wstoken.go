package auth

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// WSTokenStore manages WebSocket CSRF tokens
// Tokens are one-time use and expire after a short TTL
type WSTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]*wsTokenEntry
}

type wsTokenEntry struct {
	username  string
	createdAt time.Time
	usedAt    *time.Time // Time of first use
	useCount  int        // Number of times token was used
}

const (
	// WSTokenTTL is how long a token is valid
	WSTokenTTL = 60 * time.Second
	// WSTokenGracePeriod allows reconnection within this time after first use
	WSTokenGracePeriod = 10 * time.Second
	// WSTokenMaxUses is maximum number of uses allowed during grace period
	WSTokenMaxUses = 3
	// WSTokenLength is the byte length of the token (will be hex encoded to 2x)
	WSTokenLength = 32
)

// NewWSTokenStore creates a new WebSocket token store
func NewWSTokenStore() *WSTokenStore {
	store := &WSTokenStore{
		tokens: make(map[string]*wsTokenEntry),
	}
	// Start cleanup goroutine
	go store.cleanupLoop()
	return store
}

// Generate creates a new one-time token for a user
func (s *WSTokenStore) Generate(username string) (string, error) {
	bytes := make([]byte, WSTokenLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	s.mu.Lock()
	s.tokens[token] = &wsTokenEntry{
		username:  username,
		createdAt: time.Now(),
	}
	s.mu.Unlock()

	return token, nil
}

// Validate checks if a token is valid and marks it as used
// Allows multiple uses within grace period for reconnection scenarios
// Returns the username associated with the token, or empty string if invalid
func (s *WSTokenStore) Validate(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.tokens[token]
	if !exists {
		return "", false
	}

	now := time.Now()
	username := entry.username

	// Check if token has expired (from creation time)
	if now.Sub(entry.createdAt) > WSTokenTTL {
		delete(s.tokens, token)
		return "", false
	}

	// First use - mark the time and allow
	if entry.usedAt == nil {
		entry.usedAt = &now
		entry.useCount = 1
		return username, true
	}

	// Token already used - check grace period
	timeSinceFirstUse := now.Sub(*entry.usedAt)

	// Grace period expired - delete token
	if timeSinceFirstUse > WSTokenGracePeriod {
		delete(s.tokens, token)
		return "", false
	}

	// Check use count limit
	if entry.useCount >= WSTokenMaxUses {
		delete(s.tokens, token)
		return "", false
	}

	// Allow use within grace period (reconnection scenario)
	entry.useCount++
	return username, true
}

// cleanupLoop periodically removes expired tokens
func (s *WSTokenStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanup()
	}
}

// cleanup removes all expired tokens
func (s *WSTokenStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for token, entry := range s.tokens {
		// Remove if token creation TTL expired
		if now.Sub(entry.createdAt) > WSTokenTTL {
			delete(s.tokens, token)
			continue
		}

		// Remove if token was used and grace period expired
		if entry.usedAt != nil && now.Sub(*entry.usedAt) > WSTokenGracePeriod {
			delete(s.tokens, token)
		}
	}
}
