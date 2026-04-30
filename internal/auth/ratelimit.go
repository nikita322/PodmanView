package auth

import (
	"sync"
	"time"
)

// LoginRateLimiter limits login attempts per IP
type LoginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]*ipAttempts
	// Config
	maxAttempts int           // Max attempts before blocking
	window      time.Duration // Time window for counting attempts
	blockTime   time.Duration // How long to block after max attempts
}

type ipAttempts struct {
	count     int
	firstTime time.Time
	blocked   bool
	blockEnd  time.Time
}

// NewLoginRateLimiter creates a new rate limiter
// Default: 5 attempts per 2 minutes, block for 5 minutes
func NewLoginRateLimiter() *LoginRateLimiter {
	rl := &LoginRateLimiter{
		attempts:    make(map[string]*ipAttempts),
		maxAttempts: 5,
		window:      2 * time.Minute,
		blockTime:   5 * time.Minute,
	}

	// Start cleanup goroutine
	go rl.cleanup()

	return rl
}

// Allow checks if IP is allowed to attempt login
// Returns (allowed, remainingSeconds until unblock)
func (rl *LoginRateLimiter) Allow(ip string) (bool, int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	att, exists := rl.attempts[ip]

	if !exists {
		rl.attempts[ip] = &ipAttempts{
			count:     1,
			firstTime: now,
		}
		return true, 0
	}

	// Check if blocked
	if att.blocked {
		if now.After(att.blockEnd) {
			// Block expired, reset
			att.blocked = false
			att.count = 1
			att.firstTime = now
			return true, 0
		}
		remaining := int(att.blockEnd.Sub(now).Seconds())
		return false, remaining
	}

	// Check if window expired
	if now.Sub(att.firstTime) > rl.window {
		// Reset window
		att.count = 1
		att.firstTime = now
		return true, 0
	}

	// Increment counter
	att.count++

	// Check if exceeded
	if att.count > rl.maxAttempts {
		att.blocked = true
		att.blockEnd = now.Add(rl.blockTime)
		remaining := int(rl.blockTime.Seconds())
		return false, remaining
	}

	return true, 0
}

// Reset clears the rate limit for an IP (e.g., after successful login)
func (rl *LoginRateLimiter) Reset(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.attempts, ip)
}

// cleanup removes old entries periodically
func (rl *LoginRateLimiter) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, att := range rl.attempts {
			// Remove if: not blocked and window expired, or block expired
			if !att.blocked && now.Sub(att.firstTime) > rl.window {
				delete(rl.attempts, ip)
			} else if att.blocked && now.After(att.blockEnd) {
				delete(rl.attempts, ip)
			}
		}
		rl.mu.Unlock()
	}
}
