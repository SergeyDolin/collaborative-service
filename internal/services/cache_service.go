package services

import (
	"strings"
	"time"

	"go.uber.org/zap"
)

// CacheService предоставляет кэширование
type CacheService struct {
	cache  map[string]*CacheEntry
	ttl    time.Duration
	logger *zap.SugaredLogger
}

// CacheEntry запись в кэше
type CacheEntry struct {
	Data      interface{}
	ExpiresAt time.Time
}

// NewCacheService создает новый сервис кэширования
func NewCacheService(ttl time.Duration, logger *zap.SugaredLogger) *CacheService {
	cs := &CacheService{
		cache:  make(map[string]*CacheEntry),
		ttl:    ttl,
		logger: logger,
	}

	// Запускаем cleanup горутину
	go cs.cleanupExpired()

	return cs
}

// Get получает значение из кэша
func (cs *CacheService) Get(key string) (interface{}, bool) {
	entry, exists := cs.cache[key]
	if !exists {
		return nil, false
	}

	if time.Now().After(entry.ExpiresAt) {
		delete(cs.cache, key)
		return nil, false
	}

	cs.logger.Debugf("Cache hit: %s", key)
	return entry.Data, true
}

// Set сохраняет значение в кэш
func (cs *CacheService) Set(key string, value interface{}) {
	cs.cache[key] = &CacheEntry{
		Data:      value,
		ExpiresAt: time.Now().Add(cs.ttl),
	}
	cs.logger.Debugf("Cache set: %s", key)
}

// InvalidatePrefix инвалидирует все ключи с заданным префиксом
func (cs *CacheService) InvalidatePrefix(prefix string) {
	count := 0
	for key := range cs.cache {
		if strings.HasPrefix(key, prefix) {
			delete(cs.cache, key)
			count++
		}
	}
	cs.logger.Debugf("Invalidated %d cache entries with prefix: %s", count, prefix)
}

// InvalidateAll очищает весь кэш
func (cs *CacheService) InvalidateAll() {
	cs.cache = make(map[string]*CacheEntry)
	cs.logger.Debug("Cache cleared")
}

// cleanupExpired удаляет устаревшие записи
func (cs *CacheService) cleanupExpired() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		count := 0
		for key, entry := range cs.cache {
			if now.After(entry.ExpiresAt) {
				delete(cs.cache, key)
				count++
			}
		}
		if count > 0 {
			cs.logger.Debugf("Cleanup: removed %d expired entries", count)
		}
	}
}
