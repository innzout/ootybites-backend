package redis

import (
	"context"
	"time"
)

// AllowN implements a fixed-window counter: it increments `key` and sets the
// window TTL on the first hit, returning whether the count is still within
// `max`. Callers should treat a non-nil error as "allow" (fail open) so a Redis
// outage degrades gracefully rather than blocking users.
func (c *Client) AllowN(ctx context.Context, key string, max int, window time.Duration) (bool, error) {
	n, err := c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true, err
	}
	if n == 1 {
		// First request in this window — start the countdown.
		c.rdb.Expire(ctx, key, window)
	}
	return n <= int64(max), nil
}

// SetOTP stores an OTP code for a phone with the given TTL (typically 5 min).
func (c *Client) SetOTP(ctx context.Context, phone, code string, ttl time.Duration) error {
	return c.rdb.Set(ctx, "otp:"+phone, code, ttl).Err()
}

// GetOTP returns the active OTP for a phone, or empty string if none/expired.
func (c *Client) GetOTP(ctx context.Context, phone string) (string, error) {
	v, err := c.rdb.Get(ctx, "otp:"+phone).Result()
	if err != nil {
		return "", err
	}
	return v, nil
}

// DeleteOTP clears an OTP once consumed.
func (c *Client) DeleteOTP(ctx context.Context, phone string) error {
	return c.rdb.Del(ctx, "otp:"+phone).Err()
}
