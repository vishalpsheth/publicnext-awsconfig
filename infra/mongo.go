package infra

import (
	"context"
	"fmt"
	"log"
	"time"

	config "github.com/vishalpsheth/publicnext-awsconfig/config"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type MongoClient struct {
	Client   *mongo.Client
	Database *mongo.Database
}

// NewMongoClient initializes and connects to MongoDB
// Accepts any config type that embeds config.CoreConfig (which has MongoURI and MongoDB)
func NewMongoClient(mongoURI, mongoDB string) (*MongoClient, error) {
	if mongoURI == "" || mongoDB == "" {
		return nil, fmt.Errorf("invalid mongo config: URI and DB name required")
	}

	clientOptions := options.Client().
		ApplyURI(mongoURI).
		SetWriteConcern(writeconcern.Majority())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongo: %w", err)
	}

	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("failed to ping mongo: %w", err)
	}

	log.Printf("✅ MongoDB connected: %s (DB: %s)", config.RedactCredentials(mongoURI), mongoDB)

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
