package services

import (
	"sync"
	"time"
)

// OTPStore persists short-lived OTP codes. In-memory today (Redis is bypassed
// locally); swap for a Redis-backed impl by satisfying this interface.
type OTPStore interface {
	Set(phone, code string, ttl time.Duration)
	Get(phone string) (string, bool)
	Delete(phone string)
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
	s.entries[phone] = otpEntry{code: code, expiresAt: time.Now().Add(ttl)}
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
