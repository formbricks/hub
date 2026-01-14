package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiterEntry holds a rate limiter and its last access time for eviction
type ipLimiterEntry struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// RateLimiter implements per-IP and global rate limiting using token bucket algorithm
type RateLimiter struct {
	// Per-IP limiters with TTL tracking
	ipLimiters map[string]*ipLimiterEntry
	mu         sync.RWMutex
	perIPRate  rate.Limit
	perIPBurst int

	// Global limiter
	globalLimiter *rate.Limiter

	logger *slog.Logger
}

// NewRateLimiter creates a new rate limiter with per-IP and global limits
func NewRateLimiter(perIPRate, perIPBurst, globalRate, globalBurst int, logger *slog.Logger) *RateLimiter {
	rl := &RateLimiter{
		ipLimiters:    make(map[string]*ipLimiterEntry),
		perIPRate:     rate.Limit(perIPRate),
		perIPBurst:    perIPBurst,
		globalLimiter: rate.NewLimiter(rate.Limit(globalRate), globalBurst),
		logger:        logger,
	}

	// Start background cleanup goroutine to evict stale IP limiters
	go rl.cleanupStaleIPs()

	return rl
}

// getLimiter returns the rate limiter for a specific IP, creating one if it doesn't exist
func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	now := time.Now()

	rl.mu.RLock()
	entry, exists := rl.ipLimiters[ip]
	rl.mu.RUnlock()

	if exists {
		// Update last access time
		rl.mu.Lock()
		entry.lastAccess = now
		rl.mu.Unlock()
		return entry.limiter
	}

	// Create new limiter for this IP
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, exists := rl.ipLimiters[ip]; exists {
		entry.lastAccess = now
		return entry.limiter
	}

	limiter := rate.NewLimiter(rl.perIPRate, rl.perIPBurst)
	rl.ipLimiters[ip] = &ipLimiterEntry{
		limiter:    limiter,
		lastAccess: now,
	}

	return limiter
}

// cleanupStaleIPs periodically removes IP limiters that haven't been accessed recently
func (rl *RateLimiter) cleanupStaleIPs() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		staleThreshold := 10 * time.Minute

		for ip, entry := range rl.ipLimiters {
			if now.Sub(entry.lastAccess) > staleThreshold {
				delete(rl.ipLimiters, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Middleware returns an http.Handler middleware that enforces rate limits
func (rl *RateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract IP address
			ip := getClientIP(r)

			// Check global rate limit first (protects overall service)
			if !rl.globalLimiter.Allow() {
				rl.logger.Warn("global rate limit exceeded",
					"ip", ip,
					"path", r.URL.Path,
					"method", r.Method)

				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"Rate limit exceeded. Too many requests globally. Please try again later."}`, http.StatusTooManyRequests)
				return
			}

			// Check per-IP rate limit
			limiter := rl.getLimiter(ip)
			if !limiter.Allow() {
				rl.logger.Warn("per-IP rate limit exceeded",
					"ip", ip,
					"path", r.URL.Path,
					"method", r.Method)

				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"Rate limit exceeded. Too many requests from your IP. Please try again later."}`, http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// getClientIP extracts the client IP address from the request
// Handles X-Forwarded-For and X-Real-IP headers for proxied requests
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (standard for proxies)
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		// X-Forwarded-For can contain multiple IPs: "client, proxy1, proxy2"
		// We want the first one (original client)
		if ip := parseFirstIP(xff); ip != "" {
			return ip
		}
	}

	// Check X-Real-IP header (alternative)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := parseIP(xri); ip != "" {
			return ip
		}
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // Return as-is if parsing fails
	}

	return ip
}

// parseFirstIP extracts the first valid IP from a comma-separated list
func parseFirstIP(ips string) string {
	for i := 0; i < len(ips); i++ {
		if ips[i] == ',' {
			return parseIP(ips[:i])
		}
	}
	return parseIP(ips)
}

// parseIP validates and normalizes an IP address
func parseIP(s string) string {
	// Trim spaces
	start := 0
	end := len(s)
	for start < end && s[start] == ' ' {
		start++
	}
	for end > start && s[end-1] == ' ' {
		end--
	}

	if start >= end {
		return ""
	}

	ip := s[start:end]

	// Validate it's a valid IP
	if net.ParseIP(ip) != nil {
		return ip
	}

	return ""
}
