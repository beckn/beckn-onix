package publisher

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/beckn-one/beckn-onix/pkg/log"
	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/rabbitmq/amqp091-go"
)

// Config holds the configuration required to establish a connection with RabbitMQ.
type Config struct {
	Addr       string
	Exchange   string
	RoutingKey string
	Durable    bool
	UseTLS     bool
}

// Channel defines the interface for publishing messages to RabbitMQ.
type Channel interface {
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp091.Publishing) error
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp091.Table) error
	Close() error
}

// Publisher manages the RabbitMQ connection and channel to publish messages.
type Publisher struct {
	Conn    *amqp091.Connection
	Channel Channel
	Config  *Config
}

// Error variables representing different failure scenarios.
var (
	ErrEmptyConfig       = errors.New("empty config")
	ErrAddrMissing       = errors.New("missing required field 'Addr'")
	ErrExchangeMissing   = errors.New("missing required field 'Exchange'")
	ErrCredentialMissing = errors.New("missing RabbitMQ credentials in environment")
	ErrConnectionFailed  = errors.New("failed to connect to RabbitMQ")
	ErrChannelFailed     = errors.New("failed to open channel")
	ErrExchangeDeclare   = errors.New("failed to declare exchange")
)

// Validate checks whether the provided Config is valid for connecting to RabbitMQ.
func Validate(cfg *Config) error {
	if cfg == nil {
		return model.NewBadReqErr(fmt.Errorf("config is nil"))
	}
	if strings.TrimSpace(cfg.Addr) == "" {
		return model.NewBadReqErr(fmt.Errorf("missing config.Addr"))
	}
	if strings.TrimSpace(cfg.Exchange) == "" {
		return model.NewBadReqErr(fmt.Errorf("missing config.Exchange"))
	}
	return nil
}

// GetConnURL constructs the RabbitMQ connection URL using the config and environment credentials.
func GetConnURL(cfg *Config) (string, error) {
	user := os.Getenv("RABBITMQ_USERNAME")
	pass := os.Getenv("RABBITMQ_PASSWORD")
	if user == "" || pass == "" {
		return "", model.NewBadReqErr(fmt.Errorf("missing RabbitMQ credentials in environment"))
	}
	parts := strings.SplitN(strings.TrimSpace(cfg.Addr), "/", 2)
	hostPort := parts[0]
	vhost := "/"
	if len(parts) > 1 {
		vhost = parts[1]
	}

	if !strings.Contains(hostPort, ":") {
		if cfg.UseTLS {
			hostPort += ":5671"
		} else {
			hostPort += ":5672"
		}
	}

	encodedUser := url.QueryEscape(user)
	encodedPass := url.QueryEscape(pass)
	encodedVHost := url.QueryEscape(vhost)
	protocol := "amqp"
	if cfg.UseTLS {
		protocol = "amqps"
	}

	connURL := fmt.Sprintf("%s://%s:%s@%s/%s", protocol, encodedUser, encodedPass, hostPort, encodedVHost)
	log.Debugf(context.Background(), "Generated RabbitMQ connection details: protocol=%s, hostPort=%s, vhost=%s", protocol, hostPort, vhost)

	return connURL, nil
}

// Publish sends a message to the configured RabbitMQ exchange with the specified routing key.
// If routingKey is empty, the default routing key from Config is used.
func (p *Publisher) Publish(ctx context.Context, routingKey string, msg []byte) error {
	if routingKey == "" {
		routingKey = p.Config.RoutingKey
	}
	log.Debugf(ctx, "Attempting to publish message. Exchange: %s, RoutingKey: %s", p.Config.Exchange, routingKey)
	err := p.Channel.PublishWithContext(
		ctx,
		p.Config.Exchange,
		routingKey,
		false,
		false,
		amqp091.Publishing{
			ContentType: "application/json",
			Body:        msg,
		},
	)

	if err != nil {
		log.Errorf(ctx, err, "Publish failed for Exchange: %s, RoutingKey: %s", p.Config.Exchange, routingKey)
		return model.NewBadReqErr(fmt.Errorf("publish message failed: %w", err))
	}

	log.Infof(ctx, "Message published successfully to Exchange: %s, RoutingKey: %s", p.Config.Exchange, routingKey)
	return nil
}

// DialFunc is a function variable used to establish a connection to RabbitMQ.
var DialFunc = amqp091.Dial

// ChannelFunc is a function variable used to open a channel on the given RabbitMQ connection.
var ChannelFunc = func(conn *amqp091.Connection) (Channel, error) {
	return conn.Channel()
}

// New initializes a new Publisher with the given config, opens a connection,
// channel, and declares the exchange. Returns the publisher and a cleanup function.
func New(cfg *Config) (*Publisher, func() error, error) {
	// Step 1: Validate config
	if err := Validate(cfg); err != nil {
		return nil, nil, err
	}

	// Step 2: Build connection URL
	connURL, err := GetConnURL(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	// Step 3: Dial connection
	conn, err := DialFunc(connURL)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}

	// Step 4: Open channel
	ch, err := ChannelFunc(conn)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("%w: %v", ErrChannelFailed, err)
	}

	// Step 5: Declare exchange
	if err := ch.ExchangeDeclare(
		cfg.Exchange,
		"topic",
		cfg.Durable,
		false,
		false,
		false,
		nil,
	); err != nil {
		ch.Close()
		conn.Close()
		return nil, nil, fmt.Errorf("%w: %v", ErrExchangeDeclare, err)
	}

	// Step 6: Construct publisher
	pub := &Publisher{
		Conn:    conn,
		Channel: ch,
		Config:  cfg,
	}

	cleanup := func() error {
		if ch != nil {
			_ = ch.Close()
		}
		if conn != nil {
			return conn.Close()
		}
		return nil
	}

	return pub, cleanup, nil
}
