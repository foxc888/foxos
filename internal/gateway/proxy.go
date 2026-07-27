package gateway

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

const InternalProxyHeader = "X-Foxos-Internal-Gateway"

type ServerConfig struct {
	InternalListen     string
	PublicListen       string
	HTTPRedirectListen string
	PublicHostname     string
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

func RedirectToHTTPS(hostname string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + hostname + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

func Serve(config ServerConfig) error {
	if config.Backend == nil || config.PublicHostname == "" || config.CertificatePath == "" || config.KeyPath == "" || len(config.ProxyToken) < 32 {
		return errors.New("complete HTTPS gateway configuration is required")
	}
	internal := &http.Server{Addr: config.InternalListen, Handler: config.Backend, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20}
	target, err := url.Parse("http://" + config.InternalListen)
	if err != nil {
		return err
	}
	public := &http.Server{
		Addr:              config.PublicListen,
		Handler:           SecureProxy(target, config.ProxyToken),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	redirect := &http.Server{Addr: config.HTTPRedirectListen, Handler: RedirectToHTTPS(config.PublicHostname), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20}
	errorsChannel := make(chan error, 3)
	go func() { errorsChannel <- fmt.Errorf("internal listener: %w", internal.ListenAndServe()) }()
	go func() {
		errorsChannel <- fmt.Errorf("HTTPS listener: %w", public.ListenAndServeTLS(config.CertificatePath, config.KeyPath))
	}()
	go func() { errorsChannel <- fmt.Errorf("HTTP redirect listener: %w", redirect.ListenAndServe()) }()
	return <-errorsChannel
}
