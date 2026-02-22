// Package cache provides a generic loader cache combining LRU storage with
// singleflight to coalesce concurrent loads for the same key.
package cache

import (
	"context"

	"github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"
)

// LoaderCache is a generic cache that loads values on miss via a callback and
// coalesces concurrent loads for the same key using singleflight. Without singleflight,
// a burst of N concurrent misses for the same key would trigger N loads; with it, one
// load runs and the rest wait for and share that result.
// Keys are converted to strings internally via keyToString for LRU and singleflight.
type LoaderCache[K comparable, V any] struct {
	lru         *lru.Cache[string, V]
	group       singleflight.Group
	keyToString func(K) string
}

// NewLoaderCache creates a loader cache with the given max entries and key serializer.
func NewLoaderCache[K comparable, V any](maxEntries int, keyToString func(K) string) (*LoaderCache[K, V], error) {
	lruCache, err := lru.New[string, V](maxEntries)
	if err != nil {
		return nil, err
	}

	return &LoaderCache[K, V]{
		lru:         lruCache,
		keyToString: keyToString,
	}, nil
}

// Get returns the value for key, loading it via load on cache miss.
// On miss, Do(key, fn) ensures only one goroutine runs load() for that key;
// others block and receive the same result (request coalescing / cache stampede prevention).
func (c *LoaderCache[K, V]) Get(ctx context.Context, key K, load func(context.Context, K) (V, error)) (V, error) {
	keyStr := c.keyToString(key)
	if v, ok := c.lru.Get(keyStr); ok {
		return v, nil
	}

	val, err, _ := c.group.Do(keyStr, func() (any, error) {
		loaded, loadErr := load(ctx, key)
		if loadErr != nil {
			return zero[V](), loadErr
		}

		c.lru.Add(keyStr, loaded)

		return loaded, nil
	})
	if err != nil {
		return zero[V](), err
	}

	return val.(V), nil
}

// GetWithStats is like Get but also returns whether the value came from cache (hit) or was loaded (miss).
// Useful for metrics without pushing metrics into the cache package.
func (c *LoaderCache[K, V]) GetWithStats(ctx context.Context, key K, load func(context.Context, K) (V, error)) (V, bool, error) {
	keyStr := c.keyToString(key)
	if v, ok := c.lru.Get(keyStr); ok {
		return v, true, nil
	}

	val, err, _ := c.group.Do(keyStr, func() (any, error) {
		loaded, loadErr := load(ctx, key)
		if loadErr != nil {
			return zero[V](), loadErr
		}

		c.lru.Add(keyStr, loaded)

		return loaded, nil
	})
	if err != nil {
		return zero[V](), false, err
	}

	return val.(V), false, nil
}

func zero[V any]() (z V) { return z }

// Invalidate removes the entry for key.
func (c *LoaderCache[K, V]) Invalidate(key K) {
	c.lru.Remove(c.keyToString(key))
}

// InvalidateAll removes all entries.
func (c *LoaderCache[K, V]) InvalidateAll() {
	c.lru.Purge()
}

// Len returns the number of entries in the cache.
func (c *LoaderCache[K, V]) Len() int {
	return c.lru.Len()
}
