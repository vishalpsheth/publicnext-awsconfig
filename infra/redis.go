package infra

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisClient struct {
	*redis.Client
}

// NewRedisClient initializes and connects to Redis
func NewRedisClient(redisAddr string) (*RedisClient, error) {
	if redisAddr == "" {
		return nil, fmt.Errorf("invalid redis config: address required")
	}

	opts, err := getRedisOptions(redisAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis options: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis connection failed (Addr: %s): %w", redisAddr, err)
	}

	log.Printf("✅ Redis connected successfully: %s", redisAddr)
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

	// Simple "host:port" format
	return &redis.Options{Addr: addr, DB: 0}, nil
}

// Close handles graceful disconnection
func (r *RedisClient) Close() error {
	if r.Client != nil {
		return r.Client.Close()
	}
	return nil
}
