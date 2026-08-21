package client

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMetadataCacheDiagnosticsCountsOnlyLiveEntriesWithoutKeys(t *testing.T) {
	cache := newMetadataCache[string](5 * time.Minute)
	now := time.Now()
	cache.entries["https://user:password@example.com?access_token=secret"] = metadataCacheEntry[string]{
		value:     "live",
		expiresAt: now.Add(4 * time.Minute),
	}
	cache.entries["sk-expired-secret"] = metadataCacheEntry[string]{
		value:     "expired",
		expiresAt: now.Add(-time.Minute),
	}

	snapshot := cache.diagnostics(now)
	if snapshot.EntryCount != 1 || snapshot.TTLSeconds != 300 {
		t.Fatalf("unexpected cache diagnostics %#v", snapshot)
	}
	if snapshot.OldestStoredAtUnixMS <= 0 || snapshot.NextExpiryAtUnixMS <= now.UnixMilli() {
		t.Fatalf("unexpected cache timestamps %#v", snapshot)
	}
	if len(cache.entries) != 2 {
		t.Fatalf("read-only diagnostics mutated cache entries: %#v", cache.entries)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, forbidden := range []string{"example.com", "access_token", "secret", "sk-expired"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("cache diagnostics leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestMetadataCacheKeyHashesCompleteIdentity(t *testing.T) {
	key := metadataCacheKey("openai", "https://user:password@example.com/v1?access_token=query-secret", "sk-secret")
	if len(key) != 64 {
		t.Fatalf("cache key length = %d, want sha256 hex", len(key))
	}
	for _, forbidden := range []string{"openai", "example.com", "password", "access_token", "query-secret", "sk-secret"} {
		if strings.Contains(key, forbidden) {
			t.Fatalf("cache key leaked %q: %s", forbidden, key)
		}
	}
	other := metadataCacheKey("openai", "https://example.com/v1", "sk-other")
	if other == key {
		t.Fatal("different cache identities produced the same key")
	}
}
