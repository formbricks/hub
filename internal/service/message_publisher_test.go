package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/formbricks/hub/internal/datatypes"
)

// recordingProvider counts the events it receives.
type recordingProvider struct {
	mu     sync.Mutex
	events []Event
	done   chan struct{} // signalled per received event
}

func (p *recordingProvider) PublishEvent(_ context.Context, event Event) {
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()

	p.done <- struct{}{}
}

func (p *recordingProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.events)
}

// panickingProvider panics on every event — the misbehaving provider the fan-out must isolate.
type panickingProvider struct{}

func (panickingProvider) PublishEvent(context.Context, Event) { panic("provider exploded") }

// TestMessagePublisher_ProviderPanicIsIsolated locks the fan-out's core resilience invariant:
// one provider panicking on every event must not crash the process, kill the event loop, or
// starve the healthy providers — every event still reaches them, in order.
func TestMessagePublisher_ProviderPanicIsIsolated(t *testing.T) {
	recorder := &recordingProvider{done: make(chan struct{}, 4)}

	m := NewMessagePublisherManager(8, time.Second, nil)
	m.RegisterProvider(panickingProvider{})
	m.RegisterProvider(recorder)

	m.PublishEvent(context.Background(), datatypes.FeedbackRecordCreated, "one")
	m.PublishEvent(context.Background(), datatypes.FeedbackRecordUpdated, "two")

	for range 2 {
		select {
		case <-recorder.done:
		case <-time.After(5 * time.Second):
			t.Fatal("healthy provider starved: event never delivered after a sibling panic")
		}
	}

	m.Shutdown() // drains and stops the worker; would hang if the panic killed the loop

	require.Equal(t, 2, recorder.count(), "both events reach the healthy provider")
	assert.Equal(t, datatypes.FeedbackRecordCreated, recorder.events[0].Type, "event order preserved")
	assert.Equal(t, datatypes.FeedbackRecordUpdated, recorder.events[1].Type)
}

// TestMessagePublisher_FullChannelDropsNotBlocks locks the overflow contract: publishing into a
// full channel drops the event (ingestion is never blocked) instead of blocking the caller.
func TestMessagePublisher_FullChannelDropsNotBlocks(t *testing.T) {
	// A provider that blocks until released, so the 1-slot channel stays full.
	release := make(chan struct{})
	blocker := &blockingProvider{release: release, started: make(chan struct{})}

	m := NewMessagePublisherManager(1, time.Second, nil)
	m.RegisterProvider(blocker)

	// First event is consumed by the worker (blocking in the provider); second fills the buffer;
	// third must drop without blocking.
	m.PublishEvent(context.Background(), datatypes.FeedbackRecordCreated, "consumed")
	<-blocker.started
	m.PublishEvent(context.Background(), datatypes.FeedbackRecordCreated, "buffered")

	published := make(chan struct{})

	go func() {
		m.PublishEvent(context.Background(), datatypes.FeedbackRecordCreated, "dropped")
		close(published)
	}()

	select {
	case <-published:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a full channel; it must drop instead")
	}

	close(release)
	m.Shutdown()

	assert.Equal(t, 2, blocker.count(), "the dropped event was never delivered")
}

type blockingProvider struct {
	mu      sync.Mutex
	n       int
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (p *blockingProvider) PublishEvent(_ context.Context, _ Event) {
	p.mu.Lock()
	p.n++
	p.mu.Unlock()
	p.once.Do(func() { close(p.started) })
	<-p.release
}

func (p *blockingProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.n
}
