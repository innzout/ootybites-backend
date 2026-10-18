package services

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/innzout/ootybites/internal/redis"
)

// OTPStore persists short-lived OTP codes and single-use game run tickets.
// Two implementations below: RedisOTPStore (shared across replicas, used
// whenever REDIS_URL is set) and MemoryOTPStore (process-local dev fallback).
type OTPStore interface {
	Set(phone, code string, ttl time.Duration)
	Get(phone string) (string, bool)
	Delete(phone string)
	// Incr atomically bumps a named counter and returns its new value, starting
	// `ttl` on the first increment. Backs the OTP wrong-attempt cap.
	Incr(key string, ttl time.Duration) int
}

// MemoryOTPStore is a process-local OTP store with per-entry expiry.
type MemoryOTPStore struct {
	mu      sync.Mutex
	entries map[string]otpEntry
}

type otpEntry struct {
	code      string
	expiresAt time.Time
}

// NewMemoryOTPStore builds an in-memory OTP store.
func NewMemoryOTPStore() *MemoryOTPStore {
	return &MemoryOTPStore{entries: make(map[string]otpEntry)}
}

func (s *MemoryOTPStore) Set(phone, code string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	s.entries[phone] = otpEntry{code: code, expiresAt: time.Now().Add(ttl)}
}

// pruneLocked drops expired entries. Get only evicts the key it was asked for,
// so codes that are issued and never verified would otherwise accumulate for
// the life of the process - one small entry per distinct phone number, never
// reclaimed. Called from Set, which is the only path that grows the map.
func (s *MemoryOTPStore) pruneLocked() {
	now := time.Now()
	for k, e := range s.entries {
		if now.After(e.expiresAt) {
			delete(s.entries, k)
		}
	}
}

// Incr bumps a counter held in the same TTL map. The mutex makes the
// read-modify-write atomic within this process, which is all the in-memory
// store can promise anyway.
func (s *MemoryOTPStore) Incr(key string, ttl time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	if e, ok := s.entries[key]; ok && time.Now().Before(e.expiresAt) {
		n, _ = strconv.Atoi(e.code)
	}
	n++
	s.entries[key] = otpEntry{code: strconv.Itoa(n), expiresAt: time.Now().Add(ttl)}
	return n
}

func (s *MemoryOTPStore) Get(phone string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[phone]
	if !ok || time.Now().After(e.expiresAt) {
		delete(s.entries, phone)
		return "", false
	}
	return e.code, true
}

func (s *MemoryOTPStore) Delete(phone string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, phone)
}

// RedisOTPStore is the shared-state implementation of OTPStore.
//
// The in-memory store above is process-local, which is fine for a single
// instance but silently breaks horizontal scale: a code written on replica A is
// invisible to replica B, so a customer who requests an OTP and then verifies it
// gets "invalid code" roughly half the time behind two replicas. The same store
// backs single-use game run tickets, so those break the same way. It also loses
// every pending code on deploy.
//
// Redis is optional (REDIS_URL unset locally), so this is selected at wiring
// time and the in-memory store remains the fallback.
type RedisOTPStore struct {
	rc *redis.Client
}

// NewRedisOTPStore builds a Redis-backed OTP store.
func NewRedisOTPStore(rc *redis.Client) *RedisOTPStore { return &RedisOTPStore{rc: rc} }

// otpOpTimeout bounds each Redis round-trip. The OTP path is on a user-facing
// request, so a stalled Redis must fail fast rather than hang the login.
const otpOpTimeout = 3 * time.Second

func (s *RedisOTPStore) Set(phone, code string, ttl time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), otpOpTimeout)
	defer cancel()
	if err := s.rc.SetOTP(ctx, phone, code, ttl); err != nil {
		// Losing the write means the code we just sent can never verify, so make
		// that visible rather than letting it look like a user typo.
		slog.Error("otp store write failed", "error", err)
	}
}

func (s *RedisOTPStore) Get(phone string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), otpOpTimeout)
	defer cancel()
	code, err := s.rc.GetOTP(ctx, phone)
	if err != nil || code == "" {
		// Missing and expired are indistinguishable here, and both mean "no
		// active code" — which is the correct answer either way. Fail closed:
		// unlike rate limiting, a Redis outage must not admit a wrong code.
		return "", false
	}
	return code, true
}

func (s *RedisOTPStore) Delete(phone string) {
	ctx, cancel := context.WithTimeout(context.Background(), otpOpTimeout)
	defer cancel()
	if err := s.rc.DeleteOTP(ctx, phone); err != nil {
		slog.Warn("otp store delete failed", "error", err)
	}
}

// Incr on the Redis store is a single atomic INCR.
func (s *RedisOTPStore) Incr(key string, ttl time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), otpOpTimeout)
	defer cancel()
	n, err := s.rc.IncrWithTTL(ctx, key, ttl)
	if err != nil {
		// Fail closed: a counter we cannot read must not be treated as "no
		// attempts yet", which would remove the brute-force cap entirely.
		slog.Error("otp counter increment failed", "error", err)
		return maxOTPAttempts
	}
	return n
}
