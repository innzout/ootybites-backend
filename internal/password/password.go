// Package password wraps bcrypt hashing/verification for admin credentials.
// Matches hirzout-api's convention (bcrypt cost 12).
package password

import "golang.org/x/crypto/bcrypt"

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
