package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/formbricks/hub/internal/datatypes"
	"github.com/formbricks/hub/internal/models"
	"github.com/google/uuid"
)

// Mock repository for benchmarking
type mockWebhooksRepository struct {
	webhooks  []models.Webhook
	callCount int
}

func (m *mockWebhooksRepository) ListEnabledForEventType(ctx context.Context, eventType string) ([]models.Webhook, error) {
	m.callCount++
	// Simulate database query latency
	time.Sleep(1 * time.Millisecond)
	return m.webhooks, nil
}

// Implement other required methods with minimal overhead
func (m *mockWebhooksRepository) Create(ctx context.Context, req *models.CreateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepository) GetByID(ctx context.Context, id uuid.UUID) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepository) List(ctx context.Context, filters *models.ListWebhooksFilters) ([]models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepository) Count(ctx context.Context, filters *models.ListWebhooksFilters) (int64, error) {
	return 0, nil
}

func (m *mockWebhooksRepository) Update(ctx context.Context, id uuid.UUID, req *models.UpdateWebhookRequest) (*models.Webhook, error) {
	return nil, nil
}

func (m *mockWebhooksRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (m *mockWebhooksRepository) ListEnabled(ctx context.Context) ([]models.Webhook, error) {
	return nil, nil
}

func BenchmarkGetWebhooks_NoCache(b *testing.B) {
	repo := &mockWebhooksRepository{
		webhooks: generateTestWebhooks(10), // 10 webhooks
	}

	// Service without cache
	cacheConfig := &WebhookCacheConfig{
		Enabled: false,
	}
	service := NewWebhookDeliveryService(repo, cacheConfig)

	ctx := context.Background()
	eventType := "feedback_record.created"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := service.getWebhooksForEventType(ctx, eventType)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.Logf("Database calls: %d (expected: %d)", repo.callCount, b.N)
}

func BenchmarkGetWebhooks_WithCache(b *testing.B) {
	repo := &mockWebhooksRepository{
		webhooks: generateTestWebhooks(10), // 10 webhooks
	}

	// Service with cache
	cacheConfig := &WebhookCacheConfig{
		Enabled: true,
		Size:    100,
		TTL:     5 * time.Minute,
	}
	service := NewWebhookDeliveryService(repo, cacheConfig)

	ctx := context.Background()
	eventType := "feedback_record.created"

	// Warm up cache
	_, _ = service.getWebhooksForEventType(ctx, eventType)
	repo.callCount = 0 // Reset after warm-up

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := service.getWebhooksForEventType(ctx, eventType)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.Logf("Database calls: %d (expected: 1 for warm-up, 0 for cache hits)", repo.callCount)
	if b.N > 0 {
		cacheHitRate := (1.0 - float64(repo.callCount)/float64(b.N)) * 100
		b.Logf("Cache hit rate: %.2f%%", cacheHitRate)
	}
}

func BenchmarkGetWebhooks_MixedEventTypes(b *testing.B) {
	repo := &mockWebhooksRepository{
		webhooks: generateTestWebhooks(10),
	}

	cacheConfig := &WebhookCacheConfig{
		Enabled: true,
		Size:    100,
		TTL:     5 * time.Minute,
	}
	service := NewWebhookDeliveryService(repo, cacheConfig)

	ctx := context.Background()
	eventTypes := []string{
		"feedback_record.created",
		"feedback_record.updated",
		"feedback_record.deleted",
		"webhook.created",
		"webhook.updated",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventType := eventTypes[i%len(eventTypes)]
		_, err := service.getWebhooksForEventType(ctx, eventType)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.Logf("Database calls: %d (expected: ~%d unique event types)", repo.callCount, len(eventTypes))
}

func BenchmarkGetWebhooks_CacheExpiration(b *testing.B) {
	repo := &mockWebhooksRepository{
		webhooks: generateTestWebhooks(10),
	}

	// Cache with very short TTL to test expiration
	cacheConfig := &WebhookCacheConfig{
		Enabled: true,
		Size:    100,
		TTL:     10 * time.Millisecond, // Very short TTL
	}
	service := NewWebhookDeliveryService(repo, cacheConfig)

	ctx := context.Background()
	eventType := "feedback_record.created"

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := service.getWebhooksForEventType(ctx, eventType)
		if err != nil {
			b.Fatal(err)
		}
		// Small delay to allow some cache expiration
		if i%10 == 0 {
			time.Sleep(15 * time.Millisecond)
		}
	}

	b.Logf("Database calls: %d (cache expires frequently)", repo.callCount)
}

// BenchmarkGetWebhooks_DifferentCacheSizes tests performance with different cache sizes
func BenchmarkGetWebhooks_DifferentCacheSizes(b *testing.B) {
	sizes := []int{10, 50, 100, 200, 500}
	eventTypes := []string{
		"feedback_record.created",
		"feedback_record.updated",
		"feedback_record.deleted",
		"webhook.created",
		"webhook.updated",
		"webhook.deleted",
	}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size_%d", size), func(b *testing.B) {
			repo := &mockWebhooksRepository{
				webhooks: generateTestWebhooks(10),
			}

			cacheConfig := &WebhookCacheConfig{
				Enabled: true,
				Size:    size,
				TTL:     5 * time.Minute,
			}
			service := NewWebhookDeliveryService(repo, cacheConfig)

			ctx := context.Background()

			// Warm up cache for all event types
			for _, et := range eventTypes {
				_, _ = service.getWebhooksForEventType(ctx, et)
			}
			repo.callCount = 0

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				eventType := eventTypes[i%len(eventTypes)]
				_, err := service.getWebhooksForEventType(ctx, eventType)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.Logf("Cache size: %d, DB calls: %d, Hit rate: %.2f%%",
				size, repo.callCount, (1.0-float64(repo.callCount)/float64(b.N))*100)
		})
	}
}

// BenchmarkGetWebhooks_DifferentTTLs tests performance with different TTL values
func BenchmarkGetWebhooks_DifferentTTLs(b *testing.B) {
	ttls := []time.Duration{
		1 * time.Second,
		10 * time.Second,
		1 * time.Minute,
		5 * time.Minute,
		15 * time.Minute,
	}

	for _, ttl := range ttls {
		b.Run(fmt.Sprintf("TTL_%v", ttl), func(b *testing.B) {
			repo := &mockWebhooksRepository{
				webhooks: generateTestWebhooks(10),
			}

			cacheConfig := &WebhookCacheConfig{
				Enabled: true,
				Size:    100,
				TTL:     ttl,
			}
			service := NewWebhookDeliveryService(repo, cacheConfig)

			ctx := context.Background()
			eventType := "feedback_record.created"

			// Warm up cache
			_, _ = service.getWebhooksForEventType(ctx, eventType)
			repo.callCount = 0

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, err := service.getWebhooksForEventType(ctx, eventType)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.Logf("TTL: %v, DB calls: %d, Hit rate: %.2f%%",
				ttl, repo.callCount, (1.0-float64(repo.callCount)/float64(b.N))*100)
		})
	}
}

// BenchmarkGetWebhooks_RealisticLoad simulates realistic event frequency patterns
// High-frequency events (feedback_record.created) vs low-frequency (webhook.deleted)
func BenchmarkGetWebhooks_RealisticLoad(b *testing.B) {
	repo := &mockWebhooksRepository{
		webhooks: generateTestWebhooks(10),
	}

	cacheConfig := &WebhookCacheConfig{
		Enabled: true,
		Size:    100,
		TTL:     5 * time.Minute,
	}
	service := NewWebhookDeliveryService(repo, cacheConfig)

	ctx := context.Background()

	// Simulate realistic distribution:
	// - 70% feedback_record.created (high frequency)
	// - 20% feedback_record.updated (medium frequency)
	// - 5% feedback_record.deleted (low frequency)
	// - 3% webhook.created (very low frequency)
	// - 2% webhook.updated (very low frequency)
	// - 0.1% webhook.deleted (rare)
	eventDistribution := []struct {
		eventType string
		weight    int
	}{
		{"feedback_record.created", 700},
		{"feedback_record.updated", 200},
		{"feedback_record.deleted", 50},
		{"webhook.created", 30},
		{"webhook.updated", 20},
		{"webhook.deleted", 1},
	}

	// Build weighted event list
	var weightedEvents []string
	for _, dist := range eventDistribution {
		for i := 0; i < dist.weight; i++ {
			weightedEvents = append(weightedEvents, dist.eventType)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventType := weightedEvents[i%len(weightedEvents)]
		_, err := service.getWebhooksForEventType(ctx, eventType)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.Logf("Realistic load - DB calls: %d, Unique event types: 6, Hit rate: %.2f%%",
		repo.callCount, (1.0-float64(repo.callCount)/float64(b.N))*100)
}

// Helper function to generate test webhooks
func generateTestWebhooks(count int) []models.Webhook {
	webhooks := make([]models.Webhook, count)
	for i := 0; i < count; i++ {
		webhooks[i] = models.Webhook{
			ID:         uuid.New(),
			URL:        "https://example.com/webhook",
			SigningKey: "whsec_test",
			Enabled:    true,
			EventTypes: []datatypes.EventType{
				datatypes.FeedbackRecordCreated,
				datatypes.FeedbackRecordUpdated,
			},
		}
	}
	return webhooks
}
