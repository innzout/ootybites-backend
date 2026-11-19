// Package password wraps bcrypt hashing/verification for admin credentials.
// Matches hirzout-api's convention (bcrypt cost 12).
package password

import (
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const cost = 12

// Hash returns a bcrypt hash of the plaintext password.
func Hash(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	return string(b), err
}

// Verify reports whether plain matches the stored bcrypt hash.
func Verify(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// dummyHash is a real bcrypt hash at the production cost, computed once.
var (
	dummyOnce sync.Once
	dummyHash []byte
)

// EqualiseTiming burns the same bcrypt work a real check would.
//
// A login for an unknown username used to return immediately, while a known
// username paid for a full bcrypt comparison - roughly a quarter of a second at
// cost 12. That difference alone tells an attacker which usernames exist. Call
// this on the "no such user" path so both outcomes take the same time.
func EqualiseTiming(plain string) {
	dummyOnce.Do(func() {
		dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timing-equaliser-not-a-real-password"), cost)
	})
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(plain))
}
