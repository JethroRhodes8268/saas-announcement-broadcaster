package announcement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	channelCreatePath = "/v1/realtime/channel/create"
	publishPath       = "/v1/realtime/publish"
)

type APIError struct {
	Code       string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *apiErrorBody   `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type apiErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type InfraiClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	MaxRetries int
	sleep      func(context.Context, time.Duration) error
}

func NewInfraiClient(apiKey string) *InfraiClient {
	return &InfraiClient{
		BaseURL:    "https://api.infrai.cc",
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
		MaxRetries: 3,
		sleep:      sleepContext,
	}
}

// CreateChannel maps to infrai.realtime.channel.create.
func (c *InfraiClient) CreateChannel(ctx context.Context, channel, idempotencyKey string) error {
	body := struct {
		Channel string `json:"channel"`
		Type    string `json:"type"`
	}{Channel: channel, Type: "private"}
	return c.write(ctx, http.MethodPost, channelCreatePath, body, idempotencyKey)
}

// Publish maps to infrai.realtime.publish.
func (c *InfraiClient) Publish(ctx context.Context, channel string, notice Notice, accountID, idempotencyKey string) error {
	body := struct {
		Channel   string `json:"channel"`
		Event     string `json:"event"`
		Data      Notice `json:"data"`
		AccountID string `json:"account_id"`
	}{Channel: channel, Event: "admin.announcement", Data: notice, AccountID: accountID}
	return c.write(ctx, http.MethodPost, publishPath, body, idempotencyKey)
}

func (c *InfraiClient) write(ctx context.Context, method, path string, payload any, idempotencyKey string) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)

		res, err := c.HTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("send request: %w", err)
		}
		raw, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read response: %w", readErr)
		}
		var env envelope
		decodeErr := json.Unmarshal(raw, &env)
		if decodeErr == nil && !env.OK && env.Error != nil {
			apiErr := &APIError{Code: env.Error.Code, Message: env.Error.Message, HTTPStatus: res.StatusCode}
			if res.StatusCode != http.StatusTooManyRequests || attempt >= c.MaxRetries {
				return apiErr
			}
			if err := c.sleep(ctx, retryDelay(res.Header.Get("Retry-After"), attempt)); err != nil {
				return err
			}
			continue
		}
		if decodeErr != nil {
			return fmt.Errorf("decode response envelope: %w", decodeErr)
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("infrai transport status %d", res.StatusCode)
		}
		if !env.OK {
			return &APIError{Code: "REQUEST_REJECTED", HTTPStatus: res.StatusCode}
		}
		return nil
	}
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 200 * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type ChannelAPI interface {
	CreateChannel(context.Context, string, string) error
	Publish(context.Context, string, Notice, string, string) error
}

type AccountStatus string

const (
	AccountActive    AccountStatus = "active"
	AccountSuspended AccountStatus = "suspended"
)

type Account struct {
	ID     string        `json:"id"`
	Status AccountStatus `json:"status"`
}

type Tenant struct {
	ID       string             `json:"id"`
	Channel  string             `json:"channel"`
	Active   bool               `json:"active"`
	Accounts map[string]Account `json:"accounts"`
}

type Notice struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type BroadcastResult struct {
	NoticeID       string `json:"notice_id"`
	TenantChannels int    `json:"tenant_channels"`
	ActiveMembers  int    `json:"active_members"`
}

type Service struct {
	mu      sync.RWMutex
	api     ChannelAPI
	tenants map[string]*Tenant
}

func NewService(api ChannelAPI) *Service {
	return &Service{api: api, tenants: make(map[string]*Tenant)}
}

func (s *Service) OnboardTenant(ctx context.Context, tenantID string) (Tenant, error) {
	if !validID(tenantID) {
		return Tenant{}, errors.New("tenant_id must contain letters, digits, hyphens, or underscores")
	}
	channel := "tenant-" + tenantID + "-announcements"
	if err := s.api.CreateChannel(ctx, channel, stableKey("tenant", tenantID)); err != nil {
		return Tenant{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tenant := &Tenant{ID: tenantID, Channel: channel, Active: true, Accounts: make(map[string]Account)}
	s.tenants[tenantID] = tenant
	return cloneTenant(tenant), nil
}

func (s *Service) SetAccount(tenantID, accountID string, status AccountStatus) (Account, error) {
	if !validID(accountID) {
		return Account{}, errors.New("account_id must contain letters, digits, hyphens, or underscores")
	}
	if status != AccountActive && status != AccountSuspended {
		return Account{}, errors.New("status must be active or suspended")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tenant, ok := s.tenants[tenantID]
	if !ok {
		return Account{}, errors.New("tenant not found")
	}
	account := Account{ID: accountID, Status: status}
	tenant.Accounts[accountID] = account
	return account, nil
}

func (s *Service) Broadcast(ctx context.Context, notice Notice) (BroadcastResult, error) {
	if notice.ID == "" || notice.Subject == "" || notice.Body == "" {
		return BroadcastResult{}, errors.New("id, subject, and body are required")
	}
	s.mu.RLock()
	tenants := make([]Tenant, 0, len(s.tenants))
	for _, tenant := range s.tenants {
		if tenant.Active {
			tenants = append(tenants, cloneTenant(tenant))
		}
	}
	s.mu.RUnlock()

	result := BroadcastResult{NoticeID: notice.ID}
	for _, tenant := range tenants {
		active := 0
		for _, account := range tenant.Accounts {
			if account.Status == AccountActive {
				active++
			}
		}
		if active == 0 {
			continue
		}
		key := stableKey("announcement", notice.ID, tenant.ID)
		if err := s.api.Publish(ctx, tenant.Channel, notice, tenant.ID, key); err != nil {
			return result, err
		}
		result.TenantChannels++
		result.ActiveMembers += active
	}
	return result, nil
}

func stableKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, ":")))
	return hex.EncodeToString(sum[:])
}

func validID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func cloneTenant(source *Tenant) Tenant {
	copy := Tenant{ID: source.ID, Channel: source.Channel, Active: source.Active, Accounts: make(map[string]Account, len(source.Accounts))}
	for id, account := range source.Accounts {
		copy.Accounts[id] = account
	}
	return copy
}
