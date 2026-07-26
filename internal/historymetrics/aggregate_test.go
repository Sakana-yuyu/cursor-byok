package historymetrics

import (
	"testing"
	"time"
)

func TestFilterProviderEventsExcludesTurnFinalized(t *testing.T) {
	now := time.Date(2026, 3, 14, 12, 0, 0, 0, time.UTC)
	events := []RequestMetric{
		{Kind: KindProviderCall, At: now, Model: "m1", InputTokens: 10, TotalTokens: 15},
		{Kind: KindTurnFinalized, At: now, Model: "m1", InputTokens: 10, TotalTokens: 15},
		{Kind: KindProviderCall, At: now.Add(-2 * time.Hour), Model: "m2", InputTokens: 5, TotalTokens: 8},
	}

	filtered := FilterProviderEvents(events, now.Add(-time.Hour), now.Add(time.Hour), "")
	if len(filtered) != 1 {
		t.Fatalf("expected 1 provider event in range, got %d", len(filtered))
	}
	if filtered[0].Model != "m1" {
		t.Fatalf("unexpected model %q", filtered[0].Model)
	}

	byModel := FilterProviderEvents(events, time.Time{}, time.Time{}, "m2")
	if len(byModel) != 1 || byModel[0].Model != "m2" {
		t.Fatalf("model filter failed: %+v", byModel)
	}
}

func TestSummarizeAndBucketEvents(t *testing.T) {
	start := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)
	events := []RequestMetric{
		{Kind: KindProviderCall, At: start.Add(30 * time.Minute), InputTokens: 100, OutputTokens: 20, CacheReadTokens: 50, TotalTokens: 170},
		{Kind: KindProviderCall, At: start.Add(90 * time.Minute), InputTokens: 40, OutputTokens: 10, TotalTokens: 50},
		{Kind: KindTurnFinalized, At: start.Add(30 * time.Minute), InputTokens: 100, TotalTokens: 170},
	}
	providerOnly := FilterProviderEvents(events, start, start.Add(3*time.Hour), "")
	summary := SummarizeEvents(providerOnly, false)
	if summary.RequestCount != 2 {
		t.Fatalf("request count = %d", summary.RequestCount)
	}
	if summary.InputTokens != 140 || summary.TotalTokens != 220 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.CacheRate == nil {
		t.Fatal("expected cache rate")
	}

	buckets := BucketEvents(providerOnly, start, start.Add(3*time.Hour), 1, false)
	if len(buckets) != 3 {
		t.Fatalf("expected 3 hourly buckets, got %d", len(buckets))
	}
	if buckets[0].InputTokens != 100 || buckets[1].InputTokens != 40 || buckets[2].InputTokens != 0 {
		t.Fatalf("unexpected bucket totals: %+v", buckets)
	}
}
