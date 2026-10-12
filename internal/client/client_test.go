package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeAerugo is an identity provider and an API in one test server: /token issues tokens, /v1/... answers as the
// handler says, after checking the bearer token.
type fakeAerugo struct {
	tokens   atomic.Int32
	requests atomic.Int32
	handle   func(w http.ResponseWriter, r *http.Request, call int32)
}

func (f *fakeAerugo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		n := f.tokens.Add(1)
		if r.FormValue("grant_type") != "client_credentials" || r.FormValue("client_id") != "kl-terraform" || r.FormValue("client_secret") != "s3cret" {
			http.Error(w, `{"error":"unauthorized_client"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-" + string(rune('0'+n)), "expires_in": 300})
		return
	}
	f.handle(w, r, f.requests.Add(1))
}

func newClient(t *testing.T, f *fakeAerugo) *Client {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL + "/", TokenURL: srv.URL + "/token", ClientID: "kl-terraform", ClientSecret: "s3cret",
		ManagedWorkspace: "prod", UserAgent: "terraform-provider-aerugo/test"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestChangesSayTerraformMadeThemAndReadsDont(t *testing.T) {
	seen := map[string]string{}
	f := &fakeAerugo{handle: func(w http.ResponseWriter, r *http.Request, _ int32) {
		seen[r.Method] = r.Header.Get("Aerugo-Managed-By") + "|" + r.Header.Get("Aerugo-Managed-Workspace")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer token-") {
			t.Errorf("no bearer token on %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"slug":"payments"}`))
	}}
	c := newClient(t, f)
	var out struct{ Slug string }
	if err := c.Do(context.Background(), http.MethodPost, "/v1/workspaces", nil, map[string]string{"slug": "payments"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), http.MethodGet, "/v1/workspaces/payments", nil, nil, &out); err != nil {
		t.Fatal(err)
	}
	if seen[http.MethodPost] != "terraform|prod" {
		t.Errorf("a change didn't say Terraform made it: %q", seen[http.MethodPost])
	}
	if seen[http.MethodGet] != "|" {
		t.Errorf("a read claimed to be a change: %q", seen[http.MethodGet])
	}
	if out.Slug != "payments" {
		t.Errorf("the answer wasn't decoded: %+v", out)
	}
	if f.tokens.Load() != 1 {
		t.Errorf("signed in %d times for two requests; the token should be reused", f.tokens.Load())
	}
}

func TestARefusedTokenIsRenewedOnceAndOnlyOnce(t *testing.T) {
	f := &fakeAerugo{handle: func(w http.ResponseWriter, r *http.Request, call int32) {
		if call == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}}
	if err := newClient(t, f).Do(context.Background(), http.MethodGet, "/v1/status", nil, nil, nil); err != nil {
		t.Fatalf("one refused token should be renewed and the request retried: %v", err)
	}
	if f.tokens.Load() != 2 || f.requests.Load() != 2 {
		t.Errorf("tokens %d, requests %d; want 2 and 2", f.tokens.Load(), f.requests.Load())
	}

	always := &fakeAerugo{handle: func(w http.ResponseWriter, r *http.Request, _ int32) { w.WriteHeader(http.StatusUnauthorized) }}
	err := newClient(t, always).Do(context.Background(), http.MethodGet, "/v1/status", nil, nil, nil)
	if status(err) != http.StatusUnauthorized || always.requests.Load() != 2 {
		t.Errorf("a token refused twice should fail after one retry, not loop: %v after %d requests", err, always.requests.Load())
	}
}

func TestRefusalsCarryTheAPIsReason(t *testing.T) {
	answers := map[string]struct {
		code int
		body string
	}{
		"/v1/missing":  {http.StatusNotFound, `{"detail":"workspace not found"}`},
		"/v1/taken":    {http.StatusConflict, `{"detail":"a team with that slug already exists"}`},
		"/v1/invalid":  {http.StatusUnprocessableEntity, `{"detail":[{"loc":["body","slug"],"msg":"bad"}]}`},
		"/v1/no-json":  {http.StatusBadGateway, "upstream down"},
	}
	f := &fakeAerugo{handle: func(w http.ResponseWriter, r *http.Request, _ int32) {
		a := answers[r.URL.Path]
		w.WriteHeader(a.code)
		_, _ = w.Write([]byte(a.body))
	}}
	c := newClient(t, f)
	ctx := context.Background()
	if err := c.Do(ctx, http.MethodGet, "/v1/missing", nil, nil, nil); !IsNotFound(err) || !strings.Contains(err.Error(), "workspace not found") {
		t.Errorf("404: %v", err)
	}
	if err := c.Do(ctx, http.MethodPost, "/v1/taken", nil, map[string]string{}, nil); !IsConflict(err) || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("409: %v", err)
	}
	if err := c.Do(ctx, http.MethodPost, "/v1/invalid", nil, map[string]string{}, nil); status(err) != 422 || !strings.Contains(err.Error(), `"slug"`) {
		t.Errorf("422 should keep the validation details: %v", err)
	}
	if err := c.Do(ctx, http.MethodGet, "/v1/no-json", nil, nil, nil); status(err) != 502 || !strings.Contains(err.Error(), "upstream down") {
		t.Errorf("a plain-text refusal should come through as is: %v", err)
	}
}

func TestSettingsAreCheckedBeforeAnythingIsSent(t *testing.T) {
	for _, cfg := range []Config{
		{Endpoint: "acme.aerugo.cloud", TokenURL: "https://idp/token", ClientID: "a", ClientSecret: "b"},
		{Endpoint: "https://acme.aerugo.cloud", TokenURL: "ftp://idp/token", ClientID: "a", ClientSecret: "b"},
		{Endpoint: "https://acme.aerugo.cloud", TokenURL: "https://idp/token", ClientID: "a"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
}
