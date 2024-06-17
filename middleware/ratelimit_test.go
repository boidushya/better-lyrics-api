package middleware

import (
	"testing"
	"time"

	"golang.org/x/time/rate"
)

// TestNewIPRateLimiter tests the creation of a new IPRateLimiter.
func TestNewIPRateLimiter(t *testing.T) {
	rl := NewIPRateLimiter(1, 5)
	if rl == nil {
		t.Errorf("Expected IPRateLimiter to be created, got nil")
	}
	if rl.r != 1 {
		t.Errorf("Expected rate limit to be 1, got %v", rl.r)
	}
	if rl.b != 5 {
		t.Errorf("Expected burst limit to be 5, got %v", rl.b)
	}
}

// TestAddIP tests adding a new IP to the rate limiter.
func TestAddIP(t *testing.T) {
	rl := NewIPRateLimiter(1, 5)
	ip := "192.168.1.1"
	limiter := rl.AddIP(ip)
	if limiter == nil {
		t.Errorf("Expected rate limiter to be created for IP, got nil")
	}
	if _, exists := rl.ips[ip]; !exists {
		t.Errorf("Expected IP to be added to ips map, but it was not found")
	}
}

// TestGetLimiter tests retrieving the rate limiter for an IP.
func TestGetLimiter(t *testing.T) {
	rl := NewIPRateLimiter(1, 5)
	ip := "192.168.1.1"
	limiter := rl.GetLimiter(ip)
	if limiter == nil {
		t.Errorf("Expected rate limiter to be returned, got nil")
	}
	if _, exists := rl.ips[ip]; !exists {
		t.Errorf("Expected IP to be in ips map, but it was not found")
	}
}

// TestRateLimiting tests the actual rate limiting functionality.
func TestRateLimiting(t *testing.T) {
	rl := NewIPRateLimiter(rate.Limit(1), 1) // 1 request per second with a burst of 1
	ip := "192.168.1.1"
	limiter := rl.GetLimiter(ip)

	// Allow the first request
	if !limiter.Allow() {
		t.Errorf("Expected first request to be allowed")
	}

	// Second request should not be allowed immediately
	if limiter.Allow() {
		t.Errorf("Expected second request to be denied due to rate limiting")
	}

	// Wait for 1 second and then the request should be allowed again
	time.Sleep(1 * time.Second)
	if !limiter.Allow() {
		t.Errorf("Expected request to be allowed after waiting")
	}
}
