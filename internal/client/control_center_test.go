package client

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGetControlCenterOverviewIsolatesAccountErrors(t *testing.T) {
	service := &ProxyService{}
	overview, err := service.GetControlCenterOverview()
	if err != nil {
		t.Fatalf("GetControlCenterOverview() error = %v", err)
	}
	if overview.Accounts.State != "error" {
		t.Fatalf("accounts.state = %q, want error", overview.Accounts.State)
	}
	if overview.RequestLab.State != "unavailable" || overview.Routing.State != "unavailable" || overview.Agents.State != "unavailable" || overview.Profiles.State != "unavailable" {
		t.Fatalf("unfinished domains should stay unavailable: %+v", overview)
	}
	raw, err := json.Marshal(overview)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	lower := strings.ToLower(string(raw))
	for _, banned := range []string{"accessToken", "refreshToken", "cookie", "authorization"} {
		if strings.Contains(lower, strings.ToLower(banned)) {
			t.Fatalf("overview json contains %s: %s", banned, raw)
		}
	}
}
