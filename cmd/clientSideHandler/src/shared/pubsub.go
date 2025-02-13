package handlers

import (
	"beckn-onix/cmd/clientSideReciever/src/config"
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
)

// Initialize a new PubSub client and topic publisher
var (
	client *pubsub.Client
	topic  *pubsub.Topic
)

func init() {
	// Set up the Pub/Sub client
	_, filename, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(filepath.Dir(filename))
	credentialsPath := filepath.Join(baseDir, "../../../shared/constants", "services.json")
	fmt.Println("credentialsPath: ", credentialsPath)

	// Create the Pub/Sub client
	var err error // Declare err separately
	client, err = pubsub.NewClient(context.Background(), config.GetProjectID(), option.WithCredentialsFile(credentialsPath))
	if err != nil {
		log.Fatalf("Failed to create PubSub client: %v", err)
	}

	// Set the topic you want to publish to
	topic = client.Topic(config.GetTopicID())
}

// PublishMessage publishes a message to the specified Pub/Sub topic
func PublishMessage(payload string) error {
	ctx := context.Background()

	if len(payload) == 0 {
		fmt.Println("Cannot publish an empty message", payload)
	}

	// Create a Pub/Sub message with the payload
	result := topic.Publish(ctx, &pubsub.Message{
		Data: []byte(payload),
	})

	// Wait for the result
	id, err := result.Get(ctx)
	if err != nil {
		return fmt.Errorf("Failed to publish message here: %v", err)
	}

	log.Printf("Published message with ID: %v", id)
	return nil
}

func SubscribeToMessages(subscriptionID string) {
	fmt.Println("Subscribe to messages")
	ctx := context.Background()

	defer client.Close()

	// Get the subscription
	sub := client.Subscription(subscriptionID)

	// Receive messages
	err := sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {

		action := msg.Attributes["action"]
		// Extract the data (payload) from the message
		data := string(msg.Data)

		log.Printf("Received action: %s, data: %s", action, data)
		processMessage(string(msg.Data))
		msg.Ack()
	})

	if err != nil {
		log.Fatalf("Failed to receive messages: %v", err)
	}
}

// processMessage performs operations based on the content of the message
func processMessage(message string) {
	if message == "action_1" {

		log.Println("Performing operation for action_1")
	} else if message == "action_2" {

		log.Println("Performing operation for action_2")
	} else {
		log.Println("Unknown action received:", message)
	}
}

// Close the PubSub client when done
func Close() {
	if err := client.Close(); err != nil {
		log.Fatalf("Failed to close PubSub client: %v", err)
	}
}
