// Package client talks to Aerugo's REST API (/v1) as a service account: it signs in with the OAuth
// client-credentials flow, and every change it sends says Terraform made it, so the app can show what Terraform
// manages (slices T1 and T2 in the platform's repository).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Config is what the provider needs to reach one Aerugo deployment: self-hosted or SaaS, only these change.
type Config struct {
	Endpoint         string // the API's address, such as https://acme.aerugo.cloud
	TokenURL         string // the identity provider's token endpoint
	ClientID         string // the service account's OAuth client
	ClientSecret     string
	ManagedWorkspace string // optional: names this Terraform setup in the app ("Managed by Terraform, prod")
	UserAgent        string
	// Unmanaged sends changes without saying Terraform made them, the way the app's own changes arrive. Only the
	// acceptance tests set it, to change something "by hand" and check that the next plan shows the drift.
	Unmanaged bool
}

// Client is safe for concurrent use: Terraform calls resources in parallel.
type Client struct {
	cfg    Config
	http   *http.Client
	mu     sync.Mutex
	token  string
	expiry time.Time
}

// APIError is the API's own answer to a request that didn't succeed.
type APIError struct {
	Status int
	Detail string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Aerugo answered %d: %s", e.Status, e.Detail)
}

// IsNotFound reports whether the API answered 404: the thing is gone, or the caller may not see it.
func IsNotFound(err error) bool { return status(err) == http.StatusNotFound }

// IsConflict reports whether the API answered 409: the thing already exists, or is busy.
func IsConflict(err error) bool { return status(err) == http.StatusConflict }

func status(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status
	}
	return 0
}

// New checks the settings and returns a client. It doesn't sign in until the first request.
func New(cfg Config) (*Client, error) {
	for name, value := range map[string]string{"endpoint": cfg.Endpoint, "token_url": cfg.TokenURL} {
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, fmt.Errorf("%s must be an http or https address, such as https://acme.aerugo.cloud", name)
		}
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, errors.New("client_id and client_secret are both needed to sign in")
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	return &Client{cfg: cfg, http: &http.Client{Timeout: 60 * time.Second}}, nil
}

// accessToken returns a token from the identity provider, by the client-credentials flow, renewed a minute before it
// expires. fresh forces a new one (after the API refused the cached one).
func (c *Client) accessToken(ctx context.Context, fresh bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !fresh && c.token != "" && time.Now().Before(c.expiry.Add(-time.Minute)) {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("signing in: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("signing in: the identity provider answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return "", fmt.Errorf("signing in: %w", err)
	}
	if t.AccessToken == "" {
		return "", errors.New("signing in: the identity provider returned no token")
	}
	c.token, c.expiry = t.AccessToken, time.Now().Add(time.Duration(t.ExpiresIn)*time.Second)
	return c.token, nil
}

// Do sends one request to the API and decodes its answer into out, when out isn't nil. Every change says Terraform
// sent it (Aerugo-Managed-By). If the API refuses the token, Do signs in again once and retries.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		payload = b
	}
	target := c.cfg.Endpoint + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		token, err := c.accessToken(ctx, attempt > 0)
		if err != nil {
			return err
		}
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, target, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if method != http.MethodGet && !c.cfg.Unmanaged {
			req.Header.Set("Aerugo-Managed-By", "terraform")
			if c.cfg.ManagedWorkspace != "" {
				req.Header.Set("Aerugo-Managed-Workspace", c.cfg.ManagedWorkspace)
			}
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue // the token may have been revoked early: sign in again, once
		}
		if resp.StatusCode >= 300 {
			return &APIError{Status: resp.StatusCode, Detail: detail(data)}
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}

// detail is the API's reason for refusing: FastAPI's "detail", a sentence or a list of validation errors.
func detail(data []byte) string {
	var e struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(data, &e) != nil || len(e.Detail) == 0 {
		return strings.TrimSpace(string(data))
	}
	var sentence string
	if json.Unmarshal(e.Detail, &sentence) == nil {
		return sentence
	}
	return string(e.Detail)
}
