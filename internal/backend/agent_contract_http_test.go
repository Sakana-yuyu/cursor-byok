package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cursor/internal/backend/server/config"
)

func TestHostMountsAgentContractHTTP(t *testing.T) {
	store := config.NewStore(filepath.Join(t.TempDir(), "config.yaml"), "")
	cfg := config.DefaultConfig()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate backend port: %v", err)
	}
	cfg.BackendListenAddr = listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release backend port: %v", err)
	}
	if _, err := store.Save(context.Background(), cfg); err != nil {
		t.Fatalf("save isolated config: %v", err)
	}

	host, err := NewHost(store, nil)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	t.Cleanup(func() { _ = host.Stop(context.Background()) })
	if err := host.Start(); err != nil {
		t.Fatalf("Host.Start() error = %v", err)
	}

	baseURL := host.BaseURL()
	var healthBody string
	for attempt := 0; attempt < 20; attempt++ {
		response, requestErr := http.Get(baseURL + "/agent/v1/health")
		if requestErr == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				healthBody = string(body)
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(healthBody, `"contractVersion":"agent.contract.v1"`) {
		t.Fatalf("agent contract health response = %q", healthBody)
	}

	root := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	payload, err := json.Marshal(map[string]string{"rootPath": root})
	if err != nil {
		t.Fatalf("encode workspace request: %v", err)
	}
	response, err := http.Post(baseURL+"/agent/v1/workspaces", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("register workspace: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatalf("read workspace response: %v", readErr)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("workspace status = %d, body = %s", response.StatusCode, body)
	}
	if strings.Contains(string(body), root) || strings.Contains(strings.ToLower(string(body)), `"root"`) {
		t.Fatalf("workspace response leaked root path: %s", body)
	}
}
