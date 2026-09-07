package cache

import (
	"strings"
	"sync"
	"time"
)

// CacheEntry запись в кэше
type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

// HistoryCache кэш для истории обработок
type HistoryCache struct {
	cache   map[string]*CacheEntry
	mu      sync.RWMutex
	ttl     time.Duration
	maxSize int
}

// NewHistoryCache создает новый кэш
func NewHistoryCache(ttl time.Duration, maxSize int) *HistoryCache {
	cache := &HistoryCache{
		cache:   make(map[string]*CacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
	go cache.cleanup()
	return cache
}

// Get возвращает данные из кэша
func (c *HistoryCache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[key]
	if !exists {
		return nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry.Data, true
}

// Set сохраняет данные в кэш
func (c *HistoryCache) Set(key string, data interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	c.cache[key] = &CacheEntry{
		Data:      data,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

// Invalidate удаляет все записи кэша для данного пользователя.
// Ключи имеют формат "login:limit:offset", поэтому ищем по префиксу "login:".
func (c *HistoryCache) Invalidate(userLogin string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	prefix := userLogin + ":"
	for key := range c.cache {
		if strings.HasPrefix(key, prefix) {
			delete(c.cache, key)
		}
	}
}

// InvalidateAll очищает весь кэш
func (c *HistoryCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*CacheEntry)
}

// cleanup удаляет устаревшие записи каждые 5 минут
func (c *HistoryCache) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for key, entry := range c.cache {
			if now.After(entry.ExpiresAt) {
				delete(c.cache, key)
			}
		}
		c.mu.Unlock()
	}
}

// evictOldest удаляет самую старую запись при переполнении
func (c *HistoryCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	first := true

	for key, entry := range c.cache {
		if first || entry.ExpiresAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.ExpiresAt
			first = false
		}
	}

	if oldestKey != "" {
		delete(c.cache, oldestKey)
	}
}

// GetStats возвращает статистику кэша
func (c *HistoryCache) GetStats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return map[string]interface{}{
		"size":       len(c.cache),
		"maxSize":    c.maxSize,
		"ttlSeconds": c.ttl.Seconds(),
	}
}
