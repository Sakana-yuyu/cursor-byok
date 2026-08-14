//go:build benchmark

package main

import (
	"testing"

	serverconfig "cursor/internal/backend/server/config"
)

func TestSelectAdapterPrefersConfiguredAgentChannel(t *testing.T) {
	cfg := serverconfig.Config{
		LastAgentModelHash: "selected",
		ModelAdapters: []serverconfig.ModelAdapterConfig{
			{ID: "fallback", DisplayName: "fallback"},
			{ID: "selected", DisplayName: "selected"},
		},
	}

	got, ok := selectAdapter(cfg)
	if !ok {
		t.Fatal("selectAdapter() reported no adapter")
	}
	if got.ID != "selected" {
		t.Fatalf("selected adapter = %q, want selected", got.ID)
	}
}

func TestSelectAdapterFallsBackToFirstConfiguredChannel(t *testing.T) {
	cfg := serverconfig.Config{
		LastAgentModelHash: "missing",
		ModelAdapters: []serverconfig.ModelAdapterConfig{
			{ID: "first", DisplayName: "first"},
			{ID: "second", DisplayName: "second"},
		},
	}

	got, ok := selectAdapter(cfg)
	if !ok || got.ID != "first" {
		t.Fatalf("fallback = (%q, %t), want (first, true)", got.ID, ok)
	}
}

func TestSelectAdapterRejectsEmptyConfiguration(t *testing.T) {
	if _, ok := selectAdapter(serverconfig.Config{}); ok {
		t.Fatal("selectAdapter() accepted an empty configuration")
	}
}
