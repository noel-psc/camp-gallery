package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newHTTPSApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	cfg := testConfig(t.TempDir())
	cfg.PublicBaseURL = "https://camp.example.test"
	cfg.RequireHTTPS = true
	cfg.HSTSMaxAge = 31536000
	proxies, err := parseTrustedProxies("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	cfg.TrustedProxies = proxies
	app, err := NewApp(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	return app, server
}

func TestHTTPSEnforcementAndProxyHeaders(t *testing.T) {
	_, server := newHTTPSApp(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	response, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusPermanentRedirect || response.Header.Get("Location") != "https://camp.example.test/" {
		t.Fatalf("insecure GET was not redirected: %d %q", response.StatusCode, response.Header.Get("Location"))
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/admin/login", strings.NewReader("password=x"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("insecure POST was accepted: %d", response.StatusCode)
	}

	response, err = client.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health check was blocked: %d", response.StatusCode)
	}

	request, _ = http.NewRequest(http.MethodGet, server.URL+"/admin/login", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trusted proxy HTTPS request was rejected: %d", response.StatusCode)
	}
	if response.Header.Get("Strict-Transport-Security") == "" {
		t.Fatal("missing HSTS header on HTTPS response")
	}
	secureCSRF := false
	for _, cookie := range response.Cookies() {
		if cookie.Name == "camp_csrf" {
			secureCSRF = cookie.Secure
		}
	}
	if !secureCSRF {
		t.Fatal("CSRF cookie is not Secure behind HTTPS proxy")
	}
}

func TestProxyClientIPAndScheme(t *testing.T) {
	proxies, err := parseTrustedProxies("10.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{cfg: Config{TrustedProxies: proxies}}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	request.Header.Set("X-Forwarded-Proto", "https")
	if got := app.clientIP(request); got != "198.51.100.7" {
		t.Fatalf("clientIP = %q, want rightmost untrusted address", got)
	}
	if !app.secureRequest(request) {
		t.Fatal("trusted proxy HTTPS scheme was ignored")
	}

	request.RemoteAddr = "198.51.100.9:1234"
	request.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := app.clientIP(request); got != "198.51.100.9" {
		t.Fatalf("untrusted peer spoofed clientIP: %q", got)
	}
	if app.secureRequest(request) {
		t.Fatal("untrusted peer spoofed X-Forwarded-Proto")
	}
}

func TestLoadConfigHTTPSIsOptIn(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ADMIN_PASSWORD", "test-password")
	t.Setenv("SECRET_KEY", "test-secret")
	t.Setenv("ALLOW_INSECURE_HTTP", "0")
	t.Setenv("TRUSTED_PROXIES", "")
	t.Setenv("HSTS_MAX_AGE", "0")
	t.Setenv("PUBLIC_BASE_URL", "http://camp.example.test")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("legacy HTTP configuration was rejected: %v", err)
	}
	if cfg.RequireHTTPS {
		t.Fatal("HTTP configuration unexpectedly enforces HTTPS")
	}
	t.Setenv("PUBLIC_BASE_URL", "https://camp.example.test")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatalf("HTTPS without a configured proxy was rejected: %v", err)
	}
	if cfg.RequireHTTPS {
		t.Fatal("HTTPS without TRUSTED_PROXIES unexpectedly enforces redirects")
	}
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1/32")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RequireHTTPS || len(cfg.TrustedProxies) != 1 || cfg.HSTSMaxAge != 0 {
		t.Fatalf("HTTPS proxy config not applied: %+v", cfg)
	}
	t.Setenv("ALLOW_INSECURE_HTTP", "1")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RequireHTTPS {
		t.Fatal("ALLOW_INSECURE_HTTP did not override HTTPS enforcement")
	}
}

func TestParseTrustedProxies(t *testing.T) {
	networks, err := parseTrustedProxies("127.0.0.1/32, ::1/128, 10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	if len(networks) != 3 {
		t.Fatalf("parsed %d networks, want 3", len(networks))
	}
	if _, err := parseTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("invalid proxy address was accepted")
	}
}

func TestLoginRateLimitUsesForwardedClient(t *testing.T) {
	site := newTestSite(t)
	proxies, err := parseTrustedProxies("127.0.0.1/32")
	if err != nil {
		t.Fatal(err)
	}
	site.app.cfg.TrustedProxies = proxies
	post := func(forwarded, password string) int {
		values := url.Values{"csrf": {site.csrf}, "password": {password}}
		request, err := http.NewRequest(http.MethodPost, site.server.URL+"/admin/login", strings.NewReader(values.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("X-Forwarded-For", forwarded)
		response, err := site.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	for i := range 8 {
		if status := post("198.51.100.1", "wrong"); status != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d", i+1, status)
		}
	}
	if status := post("198.51.100.1", "test-password"); status != http.StatusTooManyRequests {
		t.Fatalf("locked client was not rejected: %d", status)
	}
	if status := post("198.51.100.2", "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("other client inherited lockout: %d", status)
	}
}
