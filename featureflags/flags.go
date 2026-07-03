// Package featureflags provides a MongoDB-backed feature flag client with in-memory caching.
// Flags are stored in the sys_config collection and polled at a configurable interval.
// Unknown flags default to enabled (fail-open). MongoDB failures preserve the last known cache.
package featureflags

import (
	"context"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
)

// Flag represents a feature flag document in the sys_config collection.
type Flag struct {
	Key         string    `bson:"key" json:"key"`
	Enabled     bool      `bson:"enabled" json:"enabled"`
	Description string    `bson:"description" json:"description"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`
}

// Client manages feature flag state with in-memory caching and background polling.
type Client struct {
	collection *mongo.Collection
	logger     *zap.Logger
	mu         sync.RWMutex
	cache      map[string]Flag
	interval   time.Duration
	stopCh     chan struct{}
}

// NewClient creates a feature flag client. The refreshInterval controls how often
// the client polls MongoDB for updated flags. Pass 0 for the default (30 seconds).
func NewClient(db *mongo.Database, logger *zap.Logger, refreshInterval time.Duration) *Client {
	if refreshInterval <= 0 {
		refreshInterval = 30 * time.Second
	}
	return &Client{
		collection: db.Collection("sys_config"),
		logger:     logger,
		cache:      make(map[string]Flag),
		interval:   refreshInterval,
		stopCh:     make(chan struct{}),
	}
}

// Start performs an initial flag load and begins background polling.
// Call once during service startup after MongoDB connection is established.
func (c *Client) Start(ctx context.Context) {
	c.refresh(ctx)
	go c.pollLoop()
}

// Stop terminates background polling. Safe to call multiple times.
func (c *Client) Stop() {
	select {
	case <-c.stopCh:
		// already closed
	default:
		close(c.stopCh)
	}
}

// IsEnabled returns whether a feature flag is enabled.
// Returns true (fail-open) if the flag key is not found in the cache.
func (c *Client) IsEnabled(_ context.Context, key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if f, ok := c.cache[key]; ok {
		return f.Enabled
	}
	return true // fail-open: unknown flags are enabled
}

// All returns all cached feature flags. Useful for the /internal/flags debug endpoint.
func (c *Client) All() []Flag {
	c.mu.RLock()
	defer c.mu.RUnlock()
	flags := make([]Flag, 0, len(c.cache))
	for _, f := range c.cache {
		flags = append(flags, f)
	}
	return flags
}

func (c *Client) pollLoop() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.refresh(context.Background())
		case <-c.stopCh:
			return
		}
	}
}

func (c *Client) refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cursor, err := c.collection.Find(ctx, bson.M{"key": bson.M{"$exists": true}})
	if err != nil {
		c.logger.Warn("feature flags refresh failed, using cached values", zap.Error(err))
		return
	}
	defer cursor.Close(ctx)

	newCache := make(map[string]Flag)
	for cursor.Next(ctx) {
		var f Flag
		if err := cursor.Decode(&f); err == nil && f.Key != "" {
			newCache[f.Key] = f
		}
	}

	c.mu.Lock()
	c.cache = newCache
	c.mu.Unlock()
}
