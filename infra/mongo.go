package infra

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type MongoClient struct {
	Client   *mongo.Client
	Database *mongo.Database
}

// MongoOptions configures the MongoDB connection pool.
type MongoOptions struct {
	MinPool                uint64
	MaxPool                uint64
	ConnectTimeout         time.Duration
	ServerSelectionTimeout time.Duration
}

// NewMongoClient initializes and connects to MongoDB
func NewMongoClient(mongoURI, mongoDB string, opts ...MongoOptions) (*MongoClient, error) {
	if mongoURI == "" || mongoDB == "" {
		return nil, fmt.Errorf("invalid mongo config: URI and DB name required")
	}

	var opt MongoOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Sensible defaults
	if opt.MinPool == 0 {
		opt.MinPool = 5
	}
	if opt.MaxPool == 0 {
		opt.MaxPool = 100
	}
	if opt.ConnectTimeout == 0 {
		opt.ConnectTimeout = 10 * time.Second
	}
	if opt.ServerSelectionTimeout == 0 {
		opt.ServerSelectionTimeout = 5 * time.Second
	}

	clientOptions := options.Client().
		ApplyURI(mongoURI).
		SetMinPoolSize(opt.MinPool).
		SetMaxPoolSize(opt.MaxPool).
		SetConnectTimeout(opt.ConnectTimeout).
		SetServerSelectionTimeout(opt.ServerSelectionTimeout).
		SetWriteConcern(writeconcern.Majority())

	ctx, cancel := context.WithTimeout(context.Background(), opt.ConnectTimeout)
	defer cancel()

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongo: %w", err)
	}

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("failed to ping mongo: %w", err)
	}

	return &MongoClient{
		Client:   client,
		Database: client.Database(mongoDB),
	}, nil
}

// Close handles graceful disconnection
func (m *MongoClient) Close() error {
	if m.Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return m.Client.Disconnect(ctx)
	}
	return nil
}
