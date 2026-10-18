package announcement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeAPI struct {
	published []string
}

func (f *fakeAPI) CreateChannel(context.Context, string, string) error { return nil }

func (f *fakeAPI) Publish(_ context.Context, channel string, _ Notice, _ string, _ string) error {
	f.published = append(f.published, channel)
	return nil
}

func TestCreateChannelOmitsVendor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["vendor"]; ok {
			t.Fatal("create channel request must not include a vendor")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"data":{}}`))
	}))
	defer server.Close()

	client := NewInfraiClient("test-key")
	client.BaseURL = server.URL
	if err := client.CreateChannel(context.Background(), "test-channel", "test-key"); err != nil {
		t.Fatal(err)
	}
}

func TestBroadcastSelectsActiveMembers(t *testing.T) {
	tests := []struct {
		name             string
		accounts         []Account
		wantChannels     int
		wantActiveMember int
	}{
		{name: "active members receive tenant broadcast", accounts: []Account{{ID: "a1", Status: AccountActive}, {ID: "a2", Status: AccountActive}}, wantChannels: 1, wantActiveMember: 2},
		{name: "suspended members are excluded from the count", accounts: []Account{{ID: "a1", Status: AccountActive}, {ID: "a2", Status: AccountSuspended}}, wantChannels: 1, wantActiveMember: 1},
		{name: "tenant with no active members is skipped", accounts: []Account{{ID: "a2", Status: AccountSuspended}}, wantChannels: 0, wantActiveMember: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := &fakeAPI{}
			service := NewService(api)
			if _, err := service.OnboardTenant(context.Background(), "acme"); err != nil {
				t.Fatal(err)
			}
			for _, account := range test.accounts {
				if _, err := service.SetAccount("acme", account.ID, account.Status); err != nil {
					t.Fatal(err)
				}
			}
			result, err := service.Broadcast(context.Background(), Notice{ID: "maintenance-42", Subject: "Maintenance", Body: "Starts at 02:00 UTC"})
			if err != nil {
				t.Fatal(err)
			}
			if result.TenantChannels != test.wantChannels || result.ActiveMembers != test.wantActiveMember {
				t.Fatalf("got channels=%d members=%d, want channels=%d members=%d", result.TenantChannels, result.ActiveMembers, test.wantChannels, test.wantActiveMember)
			}
			if len(api.published) != test.wantChannels {
				t.Fatalf("published %d times, want %d", len(api.published), test.wantChannels)
			}
		})
	}
}
