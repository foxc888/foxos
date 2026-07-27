package gateway

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureCertificatesCreatesAndReusesLocalCA(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	first, err := EnsureCertificates(directory, "foxos.home.arpa", "10.0.0.4", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureCertificates(directory, "foxos.home.arpa", "10.0.0.4", now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.CAFingerprint) != 64 || first.CAFingerprint != second.CAFingerprint {
		t.Fatalf("CA fingerprint changed: first=%s second=%s", first.CAFingerprint, second.CAFingerprint)
	}
	body, err := os.ReadFile(first.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(body)
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := certificate.VerifyHostname("foxos.home.arpa"); err != nil {
		t.Fatal(err)
	}
	if err := certificate.VerifyHostname("10.0.0.4"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{first.KeyPath, filepath.Join(directory, caKeyName)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("key mode for %s = %o", path, info.Mode().Perm())
		}
	}
}

func TestEnsureCertificatesRejectsPartialOrMismatchedMaterial(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, caCertificateName), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureCertificates(directory, "foxos.home.arpa", "10.0.0.4", time.Now()); err == nil {
		t.Fatal("expected partial material rejection")
	}
}
