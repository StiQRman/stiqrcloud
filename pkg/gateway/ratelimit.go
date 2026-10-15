package gateway

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IPRateLimiter provides thread-safe sliding token-bucket rate limiting per IP address.
type IPRateLimiter struct {
	mu           sync.Mutex
	visitors     map[string]*clientBucket
	readLimit    int           // max read requests per window
	writeLimit   int           // max write requests per window
	window       time.Duration // window duration
	lastCleaned  time.Time
}

type clientBucket struct {
	readCount  int
	writeCount int
	windowStart time.Time
}

// NewIPRateLimiter initializes an IP rate limiter.
func NewIPRateLimiter(readLimit, writeLimit int, window time.Duration) *IPRateLimiter {
	return &IPRateLimiter{
		visitors:    make(map[string]*clientBucket),
		readLimit:   readLimit,
		writeLimit:  writeLimit,
		window:      window,
		lastCleaned: time.Now(),
	}
}

// ExtractIP extracts the client IP address from the request.
func ExtractIP(r *http.Request) string {
	// Respect standard forward headers if present
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		if ip != "" {
			return ip
		}
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// IsLocalIP returns true if the IP is loopback or local.
func IsLocalIP(ip string) bool {
	if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" || ip == "" {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed != nil && parsed.IsLoopback() {
		return true
	}
	return false
}

// AllowWrite checks if an IP is allowed to perform a write operation (POST, PUT, PATCH, DELETE).
func (rl *IPRateLimiter) AllowWrite(ip string) bool {
	if IsLocalIP(ip) {
		return true // loopback callers are never throttled
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.cleanupLocked()

	now := time.Now()
	b, exists := rl.visitors[ip]
	if !exists || now.Sub(b.windowStart) > rl.window {
		rl.visitors[ip] = &clientBucket{
			writeCount:  1,
			windowStart: now,
		}
		return true
	}

	if b.writeCount >= rl.writeLimit {
		return false
	}

	b.writeCount++
	return true
}

// AllowRead checks if an IP is allowed to perform a read operation (GET, HEAD).
func (rl *IPRateLimiter) AllowRead(ip string) bool {
	if IsLocalIP(ip) {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.cleanupLocked()

	now := time.Now()
	b, exists := rl.visitors[ip]
	if !exists || now.Sub(b.windowStart) > rl.window {
		rl.visitors[ip] = &clientBucket{
			readCount:   1,
			windowStart: now,
		}
		return true
	}

	if b.readCount >= rl.readLimit {
		return false
	}

	b.readCount++
	return true
}

func (rl *IPRateLimiter) cleanupLocked() {
	if time.Since(rl.lastCleaned) < 2*rl.window {
		return
	}
	now := time.Now()
	for ip, b := range rl.visitors {
		if now.Sub(b.windowStart) > rl.window {
			delete(rl.visitors, ip)
		}
	}
	rl.lastCleaned = now
}
