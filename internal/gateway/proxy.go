package gateway

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

const InternalProxyHeader = "X-Foxos-Internal-Gateway"

type ServerConfig struct {
	InternalListen     string
	PublicListen       string
	HTTPRedirectListen string
	PublicHostname     string
	PublicAddress      string
	CertificatePath    string
	KeyPath            string
	ProxyToken         string
	Backend            http.Handler
}

func NewProxyToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func SecureProxy(target *url.URL, proxyToken string) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(target)
			request.Out.Host = target.Host
			request.Out.Header.Del("Forwarded")
			request.Out.Header.Del("X-Forwarded-For")
			request.Out.Header.Del("X-Forwarded-Host")
			request.Out.Header.Del("X-Forwarded-Proto")
			request.Out.Header.Del(InternalProxyHeader)
			request.SetXForwarded()
			request.Out.Header.Set("X-Forwarded-Proto", "https")
			request.Out.Header.Set(InternalProxyHeader, proxyToken)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("{\"error\":\"gateway_unavailable\",\"message\":\"FoxOS internal service is unavailable\"}\n"))
		},
	}
	return proxy
}

func allowedHost(next http.Handler, listenAddress string, hosts ...string) (http.Handler, error) {
	if next == nil {
		return nil, errors.New("host guard handler is required")
	}
	_, listenPort, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return nil, errors.New("host guard listen address is invalid")
	}
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		if normalized == "" || strings.ContainsAny(normalized, "\t\r\n,/@?#") {
			return nil, errors.New("host guard contains an invalid host")
		}
		if net.ParseIP(normalized) == nil && !validHomeArpaHostname(normalized) {
			return nil, errors.New("host guard contains an invalid host")
		}
		allowed[normalized] = struct{}{}
	}
	if len(allowed) == 0 {
		return nil, errors.New("host guard requires at least one host")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, port, valid := requestAuthority(r.Host)
		if !valid || port != "" && port != listenPort {
			problemHost(w)
			return
		}
		if _, ok := allowed[host]; !ok {
			problemHost(w)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

func requestAuthority(authority string) (string, string, bool) {
	if authority == "" || strings.TrimSpace(authority) != authority || strings.ContainsAny(authority, "\t\r\n,/@?#") {
		return "", "", false
	}
	if host, port, err := net.SplitHostPort(authority); err == nil {
		if host == "" || port == "" {
			return "", "", false
		}
		return strings.ToLower(host), port, true
	}
	if strings.Contains(authority, ":") {
		if ip := net.ParseIP(authority); ip != nil {
			return strings.ToLower(authority), "", true
		}
		return "", "", false
	}
	return strings.ToLower(authority), "", true
}

func problemHost(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusMisdirectedRequest)
	_, _ = w.Write([]byte("{\"error\":\"host_not_allowed\",\"message\":\"request host is not allowed\"}\n"))
}

func RedirectToHTTPS(hostname string) http.Handler {
	validHostname := validHomeArpaHostname(hostname)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !validHostname {
			http.Error(w, "HTTPS redirect is unavailable", http.StatusInternalServerError)
			return
		}
		target := (&url.URL{Scheme: "https", Host: hostname, Path: r.URL.Path, RawPath: r.URL.RawPath, ForceQuery: r.URL.ForceQuery, RawQuery: r.URL.RawQuery}).String()
		w.Header().Set("Location", target)
		w.WriteHeader(http.StatusPermanentRedirect)
	})
}

func validHomeArpaHostname(value string) bool {
	if value == "" || len(value) > 253 || value != strings.ToLower(value) || !strings.HasSuffix(value, ".home.arpa") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func Serve(ctx context.Context, config ServerConfig) error {
	if ctx == nil {
		return errors.New("gateway context is required")
	}
	if config.Backend == nil || config.PublicHostname == "" || config.CertificatePath == "" || config.KeyPath == "" || len(config.ProxyToken) < 32 {
		return errors.New("complete HTTPS gateway configuration is required")
	}
	internal := &http.Server{Addr: config.InternalListen, Handler: config.Backend, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	target, err := url.Parse("http://" + config.InternalListen)
	if err != nil {
		return err
	}
	publicHandler, err := allowedHost(SecureProxy(target, config.ProxyToken), config.PublicListen, config.PublicHostname, config.PublicAddress)
	if err != nil {
		return err
	}
	redirectHandler, err := allowedHost(RedirectToHTTPS(config.PublicHostname), config.HTTPRedirectListen, config.PublicHostname, config.PublicAddress)
	if err != nil {
		return err
	}
	public := &http.Server{
		Addr:              config.PublicListen,
		Handler:           publicHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	redirect := &http.Server{Addr: config.HTTPRedirectListen, Handler: redirectHandler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20}
	errorsChannel := make(chan error, 3)
	go func() { errorsChannel <- fmt.Errorf("internal listener: %w", internal.ListenAndServe()) }()
	go func() {
		errorsChannel <- fmt.Errorf("HTTPS listener: %w", public.ListenAndServeTLS(config.CertificatePath, config.KeyPath))
	}()
	go func() { errorsChannel <- fmt.Errorf("HTTP redirect listener: %w", redirect.ListenAndServe()) }()

	var serveErr error
	select {
	case serveErr = <-errorsChannel:
	case <-ctx.Done():
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := errors.Join(internal.Shutdown(shutdownContext), public.Shutdown(shutdownContext), redirect.Shutdown(shutdownContext))
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return errors.Join(serveErr, shutdownErr)
	}
	return shutdownErr
}
