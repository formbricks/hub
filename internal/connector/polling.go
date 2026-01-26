package connector

import (
	"context"
	"log/slog"
	"time"
)

// PollingInputConnector defines the interface for polling-based input connectors
type PollingInputConnector interface {
	Poll(ctx context.Context) error
}

// Poller manages periodic polling for polling input connectors
type Poller struct {
	interval time.Duration
	name     string
}

// NewPoller creates a new poller with the specified interval and name
func NewPoller(interval time.Duration, name string) *Poller {
	return &Poller{
		interval: interval,
		name:     name,
	}
}

// Start begins polling the connector at the specified interval
// It polls immediately on startup, then continues polling at the specified interval.
// The polling stops when the context is cancelled.
func (p *Poller) Start(ctx context.Context, connector PollingInputConnector) {
	go func() {
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()

		slog.Info("Starting connector poller",
			"name", p.name,
			"poll_interval", p.interval,
		)

		// Poll immediately on startup
		if err := connector.Poll(ctx); err != nil {
			slog.Error("Initial poll failed",
				"name", p.name,
				"error", err,
			)
		}

		// Then poll on schedule
		for {
			select {
			case <-ctx.Done():
				slog.Info("Connector poller shutting down", "name", p.name)
				return
			case <-ticker.C:
				if err := connector.Poll(ctx); err != nil {
					slog.Error("Poll failed",
						"name", p.name,
						"error", err,
					)
					// Continue polling even if one poll fails
				}
			}
		}
	}()
}
