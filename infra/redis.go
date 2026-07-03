package infra

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisClient struct {
	*redis.Client
}

// RedisOptions configures the Redis connection pool.
type RedisOptions struct {
	PoolSize     int
	MinIdleConns int
	DialTimeout  time.Duration
}

// NewRedisClient initializes and connects to Redis
func NewRedisClient(redisAddr string, opts ...RedisOptions) (*RedisClient, error) {
	if redisAddr == "" {
		return nil, fmt.Errorf("invalid redis config: address required")
	}

	var opt RedisOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Sensible defaults
	if opt.PoolSize == 0 {
		opt.PoolSize = 50
	}
	if opt.MinIdleConns == 0 {
		opt.MinIdleConns = 10
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 5 * time.Second
	}

	redisOpts, err := getRedisOptions(redisAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis options: %w", err)
	}

	// Apply pool settings
	redisOpts.PoolSize = opt.PoolSize
	redisOpts.MinIdleConns = opt.MinIdleConns
	redisOpts.DialTimeout = opt.DialTimeout

	client := redis.NewClient(redisOpts)

	ctx, cancel := context.WithTimeout(context.Background(), opt.DialTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis connection failed (Addr: %s): %w", redisAddr, err)
	}

	return &RedisClient{Client: client}, nil
}

// getRedisOptions generates redis.Options based on address format
func getRedisOptions(redisAddr string) (*redis.Options, error) {
	addr := strings.TrimSpace(redisAddr)
	if addr == "" {
		return nil, fmt.Errorf("empty RedisAddr")
	}

	// URL format (redis:// or rediss://)
	if strings.HasPrefix(addr, "redis://") || strings.HasPrefix(addr, "rediss://") {
		opt, err := redis.ParseURL(addr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redis url %q: %w", addr, err)
		}

		// Enforce TLS >= 1.2 for rediss://
		if strings.HasPrefix(addr, "rediss://") {
			if opt.TLSConfig == nil {
				opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			} else if opt.TLSConfig.MinVersion < tls.VersionTLS12 {
				opt.TLSConfig.MinVersion = tls.VersionTLS12
			}
		}
		return opt, nil
	}

	// Plain "host:port" format — auto-detect TLS based on IP
	opt := &redis.Options{Addr: addr, DB: 0}

	// Extract host from addr
	host := addr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		host = addr[:idx]
	}

	// Apply TLS for non-private IPs (AWS managed endpoints)
	if !isPrivateIP(host) {
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	return opt, nil
}

// isPrivateIP checks if the host is a private/local IP address
func isPrivateIP(host string) bool {
	return strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.") ||
		strings.HasPrefix(host, "172.16.") || strings.HasPrefix(host, "172.17.") ||
		strings.HasPrefix(host, "172.18.") || strings.HasPrefix(host, "172.19.") ||
		strings.HasPrefix(host, "172.20.") || strings.HasPrefix(host, "172.21.") ||
		strings.HasPrefix(host, "172.22.") || strings.HasPrefix(host, "172.23.") ||
		strings.HasPrefix(host, "172.24.") || strings.HasPrefix(host, "172.25.") ||
		strings.HasPrefix(host, "172.26.") || strings.HasPrefix(host, "172.27.") ||
		strings.HasPrefix(host, "172.28.") || strings.HasPrefix(host, "172.29.") ||
		strings.HasPrefix(host, "172.30.") || strings.HasPrefix(host, "172.31.") ||
		strings.HasPrefix(host, "127.") ||
		host == "localhost"
}

// Close handles graceful disconnection
func (r *RedisClient) Close() error {
	if r.Client != nil {
		return r.Client.Close()
	}
	return nil
}
