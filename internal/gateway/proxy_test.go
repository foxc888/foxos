package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestSecureProxyControlsForwardingHeaders(t *testing.T) {
	const proxyToken = "01234567890123456789012345678901"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("Forwarded") != "" || r.Header.Get(InternalProxyHeader) != proxyToken {
			t.Fatalf("unexpected forwarding headers: %+v", r.Header)
		}
		if got := r.Header.Get("X-Forwarded-Host"); got != "foxos.home.arpa" {
			t.Fatalf("X-Forwarded-Host=%q", got)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()
	target, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "https://foxos.home.arpa/api/v1/health/live", nil)
	request.Header.Set("Forwarded", "for=attacker")
	request.Header.Set(InternalProxyHeader, "attacker-controlled")
	response := httptest.NewRecorder()
	SecureProxy(target, proxyToken).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.Code, body)
	}
}

func TestAllowedHostRejectsForgedAuthority(t *testing.T) {
	t.Parallel()
	handler, err := allowedHost(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), ":443", "foxos.home.arpa", "10.0.0.4")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		host string
		want int
	}{
		{host: "foxos.home.arpa", want: http.StatusNoContent},
		{host: "foxos.home.arpa:443", want: http.StatusNoContent},
		{host: "10.0.0.4", want: http.StatusNoContent},
		{host: "foxos.home.arpa:444", want: http.StatusMisdirectedRequest},
		{host: "attacker.invalid", want: http.StatusMisdirectedRequest},
		{host: "foxos.home.arpa@attacker.invalid", want: http.StatusMisdirectedRequest},
	} {
		request := httptest.NewRequest(http.MethodGet, "https://foxos.home.arpa/", nil)
		request.Host = test.host
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Errorf("host=%q status=%d want=%d", test.host, response.Code, test.want)
		}
	}
}

func TestRedirectUsesCanonicalHomeArpaHost(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://attacker.invalid/path?q=1", nil)
	response := httptest.NewRecorder()
	RedirectToHTTPS("foxos.home.arpa").ServeHTTP(response, request)
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "https://foxos.home.arpa/path?q=1" {
		t.Fatalf("status=%d location=%s", response.Code, response.Header().Get("Location"))
	}
}

func TestRedirectDoesNotTreatRequestPathAsAuthority(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://attacker.invalid//other.example/path?q=1", nil)
	response := httptest.NewRecorder()
	RedirectToHTTPS("foxos.home.arpa").ServeHTTP(response, request)
	if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "https://foxos.home.arpa//other.example/path?q=1" {
		t.Fatalf("status=%d location=%s", response.Code, response.Header().Get("Location"))
	}
}

func TestRedirectRejectsInvalidPublicHostname(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://foxos.invalid/", nil)
	response := httptest.NewRecorder()
	RedirectToHTTPS("foxos.home.arpa@attacker.invalid").ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || response.Header().Get("Location") != "" {
		t.Fatalf("status=%d location=%s", response.Code, response.Header().Get("Location"))
	}
}
