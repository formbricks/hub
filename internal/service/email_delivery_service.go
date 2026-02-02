package service

import (
	"context"
	"log/slog"
)

// EmailDeliveryService is a placeholder email delivery service implementing MessagePublisher
type EmailDeliveryService struct {
	// Future: email configuration (SMTP server, API key, etc.)
}

// NewEmailDeliveryService creates a new email delivery service
func NewEmailDeliveryService() *EmailDeliveryService {
	return &EmailDeliveryService{}
}

// PublishEvent publishes a single event (placeholder - just logs)
func (e *EmailDeliveryService) PublishEvent(ctx context.Context, event Event) error {
	// Placeholder: log that email would be sent
	slog.Info("Email delivery (placeholder)",
		"event_type", event.Type,
		"timestamp", event.Timestamp,
	)
	return nil
}

// PublishEvents publishes multiple events (placeholder - just logs)
func (e *EmailDeliveryService) PublishEvents(ctx context.Context, events []Event) error {
	for _, event := range events {
		if err := e.PublishEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}
