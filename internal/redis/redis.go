// Package redis wraps the Upstash Redis client and hosts the rate-limit and
// OTP-counter helpers used by sensitive routes.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client is our Redis handle.
type Client struct {
	rdb *redis.Client
}

// New parses a redis:// (or rediss://) URL and verifies connectivity. When
// redisURL is empty it returns (nil, nil): Redis is optional, and callers fall
// back to an in-memory limiter (see internal/middleware.NewMemoryLimiter).
func New(ctx context.Context, redisURL string) (*Client, error) {
	if redisURL == "" {
		return nil, nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	rdb := redis.NewClient(opt)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Client{rdb: rdb}, nil
}

// Raw exposes the underlying client for packages that need it directly.
func (c *Client) Raw() *redis.Client { return c.rdb }

// Close releases the connection pool.
func (c *Client) Close() error { return c.rdb.Close() }
