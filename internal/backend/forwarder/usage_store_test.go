package forwarder

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newUsageMergeTestStore(t *testing.T) *UsageFileStore {
	t.Helper()
	store := NewUsageFileStore(t.TempDir())
	t.Cleanup(func() {
		if err := store.Flush(); err != nil {
			t.Errorf("cleanup Flush: %v", err)
		}
	})
	return store
}

func assertUsageMergeLookup(t *testing.T, store *UsageFileStore, id string, want int64) usageFileEvent {
	t.Helper()
	event, found, err := store.LookupEvent(id)
	if err != nil || !found || event.TotalTokens != want {
		t.Fatalf("LookupEvent(%q) = %+v, %t, %v; want tokens=%d", id, event, found, err, want)
	}
	return event
}

func TestUsageFileStorePendingUpsertKeepsLatestEvent(t *testing.T) {
	store := newUsageMergeTestStore(t)
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	for value := int64(1); value <= usageWriteMaxBatch+1; value++ {
		if err := store.UpsertEvent(usageFileEvent{
			EventID: " turn::call ", At: at, InputTokens: value,
			OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, UsagePresent: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	count := len(store.pendingEvents)
	store.mu.Unlock()
	if count != 1 {
		t.Fatalf("pending events = %d, want 1", count)
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatalf("duplicate updates unexpectedly flushed: %v", err)
	}
	want := int64(usageWriteMaxBatch + 1 + 9)
	event := assertUsageMergeLookup(t, store, "turn", want)
	if event.InputTokens != usageWriteMaxBatch+1 || event.OutputTokens != 2 || event.CacheReadTokens != 3 || event.CacheWriteTokens != 4 || !event.UsagePresent {
		t.Fatalf("unexpected aggregate: %+v", event)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	assertUsageMergeLookup(t, store, "turn", want)
	doc, err := readUsageFileDocument(store.path)
	if err != nil || doc.Totals.ProviderCalls != 1 || doc.Totals.TotalTokens != want || len(doc.RecentEvents) != 1 {
		t.Fatalf("flushed document = %+v, %v", doc, err)
	}
}

func TestUsageFileStorePendingOverridesDiskBeforeAggregation(t *testing.T) {
	store := newUsageMergeTestStore(t)
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	for _, event := range []usageFileEvent{
		{EventID: "turn::a", At: at, InputTokens: 100, UsagePresent: true},
		{EventID: "turn::b", At: at, InputTokens: 7},
		{EventID: "turn-other::a", At: at, InputTokens: 999},
	} {
		if err := store.UpsertEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	assertUsageMergeLookup(t, store, "turn", 107)
	// Last write wins even when its timestamp and token count are older/smaller.
	if err := store.UpsertEvent(usageFileEvent{EventID: "turn::a", At: at.Add(-24 * time.Hour), InputTokens: 3}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEvent(usageFileEvent{EventID: "turn::c", At: at.Add(time.Hour), OutputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	exact := assertUsageMergeLookup(t, store, "turn::a", 3)
	if exact.UsagePresent {
		t.Fatal("superseded UsagePresent must not survive replacement")
	}
	before := assertUsageMergeLookup(t, store, "turn", 15)
	if !before.At.Equal(at.Add(time.Hour)) {
		t.Fatalf("aggregate timestamp = %v", before.At)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	after := assertUsageMergeLookup(t, store, "turn", 15)
	if before != after {
		t.Fatalf("lookup changed across Flush: before=%+v after=%+v", before, after)
	}
	doc, err := readUsageFileDocument(store.path)
	if err != nil || doc.Totals.ProviderCalls != 4 || doc.Totals.TotalTokens != 1014 {
		t.Fatalf("flushed totals = %+v, %v", doc.Totals, err)
	}
	var dailyTotal int64
	for _, day := range doc.Daily {
		dailyTotal += day.TotalTokens
	}
	if dailyTotal != doc.Totals.TotalTokens {
		t.Fatalf("daily total = %d, total = %d", dailyTotal, doc.Totals.TotalTokens)
	}
}

func TestUsageFileStoreFailedFlushRetainsNewestPendingValue(t *testing.T) {
	store := newUsageMergeTestStore(t)
	if err := os.WriteFile(store.path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertEvent(usageFileEvent{EventID: "turn::call", InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err == nil {
		t.Fatal("Flush unexpectedly accepted corrupt disk data")
	}
	if err := store.UpsertEvent(usageFileEvent{EventID: "turn::call", InputTokens: 20}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.path); err != nil {
		t.Fatal(err)
	}
	assertUsageMergeLookup(t, store, "turn", 20)
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	assertUsageMergeLookup(t, store, "turn", 20)
	doc, err := readUsageFileDocument(store.path)
	if err != nil || doc.Totals.ProviderCalls != 1 || doc.Totals.TotalTokens != 20 {
		t.Fatalf("recovered document = %+v, %v", doc, err)
	}
}

func TestUsageFileStoreConcurrentLookupUpsertAndFlush(t *testing.T) {
	store := newUsageMergeTestStore(t)
	const updates = 80
	if err := store.UpsertEvent(usageFileEvent{EventID: "turn::call", InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	var latest atomic.Int64
	latest.Store(1)
	start := make(chan struct{})
	errors := make(chan error, 4)
	var workers sync.WaitGroup
	workers.Add(4)
	go func() {
		defer workers.Done()
		<-start
		for value := int64(2); value <= updates; value++ {
			if err := store.UpsertEvent(usageFileEvent{EventID: "turn::call", InputTokens: value}); err != nil {
				errors <- err
				return
			}
			latest.Store(value)
		}
	}()
	go func() {
		defer workers.Done()
		<-start
		for range updates {
			if err := store.Flush(); err != nil {
				errors <- err
				return
			}
		}
	}()
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			for range updates * 2 {
				minimum := latest.Load()
				event, found, err := store.LookupEvent("turn")
				if err != nil || !found || event.TotalTokens < minimum || event.TotalTokens > updates {
					errors <- fmt.Errorf("concurrent lookup = %+v, %t, %v; want [%d,%d]", event, found, err, minimum, updates)
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	assertUsageMergeLookup(t, store, "turn", updates)
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	assertUsageMergeLookup(t, store, "turn", updates)
	doc, err := readUsageFileDocument(store.path)
	if err != nil || doc.Totals.ProviderCalls != 1 || doc.Totals.TotalTokens != updates {
		t.Fatalf("final document = %+v, %v", doc, err)
	}
}
