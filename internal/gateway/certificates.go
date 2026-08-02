package gateway

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	caCertificateName = "foxos-local-ca.pem"
	caKeyName         = "foxos-local-ca-key.pem"
	certificateName   = "foxos.pem"
	keyName           = "foxos-key.pem"
)

type Certificates struct {
	CACertificatePath string
	CertificatePath   string
	KeyPath           string
	CACertificatePEM  []byte
	CAFingerprint     string
}

func EnsureCertificates(directory, hostname, address string, now time.Time) (Certificates, error) {
	if directory == "" || hostname == "" {
		return Certificates{}, errors.New("TLS directory and hostname are required")
	}
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil {
		return Certificates{}, errors.New("TLS address must be an IPv4 literal")
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return Certificates{}, err
	}
	if err := os.MkdirAll(absoluteDirectory, 0o700); err != nil {
		return Certificates{}, fmt.Errorf("create TLS directory: %w", err)
	}
	paths := Certificates{
		CACertificatePath: filepath.Join(absoluteDirectory, caCertificateName),
		CertificatePath:   filepath.Join(absoluteDirectory, certificateName),
		KeyPath:           filepath.Join(absoluteDirectory, keyName),
	}
	caKeyPath := filepath.Join(absoluteDirectory, caKeyName)
	allPaths := []string{paths.CACertificatePath, caKeyPath, paths.CertificatePath, paths.KeyPath}
	existing := 0
	for _, path := range allPaths {
		info, statErr := os.Lstat(path)
		if statErr == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return Certificates{}, fmt.Errorf("TLS material is not a regular file: %s", filepath.Base(path))
			}
			existing++
			continue
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return Certificates{}, statErr
		}
	}
	if existing != 0 && existing != len(allPaths) {
		return Certificates{}, errors.New("TLS material is incomplete; refusing to replace a partial local CA")
	}
	if existing == 0 {
		if err := generateCertificateSet(paths, caKeyPath, hostname, ip.To4(), now.UTC()); err != nil {
			return Certificates{}, err
		}
	}
	caCertificate, caKey, caPEM, err := loadCA(paths.CACertificatePath, caKeyPath)
	if err != nil {
		return Certificates{}, err
	}
	if now.Before(caCertificate.NotBefore) || !now.Add(400*24*time.Hour).Before(caCertificate.NotAfter) {
		return Certificates{}, errors.New("FoxOS local CA is not currently valid for at least 400 days; rotate it explicitly and redistribute trust")
	}
	leaf, leafKey, err := loadLeaf(paths.CertificatePath, paths.KeyPath)
	if err != nil {
		return Certificates{}, err
	}
	if err := validateLeaf(leaf, leafKey, caCertificate, hostname, ip.To4(), now.UTC()); err != nil {
		return Certificates{}, err
	}
	if !now.Add(30 * 24 * time.Hour).Before(leaf.NotAfter) {
		if err := generateLeaf(paths.CertificatePath, paths.KeyPath, hostname, ip.To4(), caCertificate, caKey, now.UTC()); err != nil {
			return Certificates{}, fmt.Errorf("renew TLS leaf certificate: %w", err)
		}
		leaf, leafKey, err = loadLeaf(paths.CertificatePath, paths.KeyPath)
		if err != nil {
			return Certificates{}, err
		}
		if err := validateLeaf(leaf, leafKey, caCertificate, hostname, ip.To4(), now.UTC()); err != nil {
			return Certificates{}, err
		}
	}
	_ = leafKey
	digest := sha256.Sum256(caCertificate.Raw)
	paths.CACertificatePEM = caPEM
	paths.CAFingerprint = hex.EncodeToString(digest[:])
	return paths, nil
}

func generateCertificateSet(paths Certificates, caKeyPath, hostname string, address net.IP, now time.Time) error {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "FoxOS Local CA", Organization: []string{"FoxOS local network"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return err
	}
	if err := writePEM(paths.CACertificatePath, 0o644, "CERTIFICATE", caDER, false); err != nil {
		return err
	}
	if err := writePEM(caKeyPath, 0o600, "PRIVATE KEY", caKeyDER, false); err != nil {
		return err
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	return generateLeaf(paths.CertificatePath, paths.KeyPath, hostname, address, caCertificate, caKey, now)
}

func generateLeaf(certificatePath, keyPath, hostname string, address net.IP, ca *x509.Certificate, caKey *ecdsa.PrivateKey, now time.Time) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := randomSerial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hostname, Organization: []string{"FoxOS local network"}},
		DNSNames:     []string{hostname},
		IPAddresses:  []net.IP{append(net.IP(nil), address...)},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(397 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writePEM(certificatePath, 0o644, "CERTIFICATE", certificateDER, true); err != nil {
		return err
	}
	return writePEM(keyPath, 0o600, "PRIVATE KEY", keyDER, true)
}

func loadCA(certificatePath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, []byte, error) {
	certificatePEM, certificate, err := loadCertificate(certificatePath)
	if err != nil {
		return nil, nil, nil, err
	}
	key, err := loadPrivateKey(keyPath)
	if err != nil {
		return nil, nil, nil, err
	}
	if !certificate.IsCA || !publicKeysEqual(certificate.PublicKey, &key.PublicKey) {
		return nil, nil, nil, errors.New("FoxOS local CA certificate and key do not match")
	}
	return certificate, key, certificatePEM, nil
}

func loadLeaf(certificatePath, keyPath string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	_, certificate, err := loadCertificate(certificatePath)
	if err != nil {
		return nil, nil, err
	}
	key, err := loadPrivateKey(keyPath)
	if err != nil {
		return nil, nil, err
	}
	if !publicKeysEqual(certificate.PublicKey, &key.PublicKey) {
		return nil, nil, errors.New("FoxOS TLS certificate and key do not match")
	}
	return certificate, key, nil
}

func loadCertificate(path string) ([]byte, *x509.Certificate, error) {
	body, err := readTLSMaterial(path)
	if err != nil {
		return nil, nil, err
	}
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, fmt.Errorf("invalid certificate PEM: %s", filepath.Base(path))
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return body, certificate, nil
}

func loadPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	body, err := readTLSMaterial(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(body)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("invalid private key PEM: %s", filepath.Base(path))
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ecdsaKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("FoxOS TLS key must be ECDSA")
	}
	return ecdsaKey, nil
}

func readTLSMaterial(path string) ([]byte, error) {
	const limit = 1 << 20
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return nil, errors.New("TLS material is not a bounded regular file")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || len(body) > limit {
		return nil, errors.New("TLS material size changed while reading")
	}
	return body, nil
}

func validateLeaf(leaf *x509.Certificate, key *ecdsa.PrivateKey, ca *x509.Certificate, hostname string, address net.IP, now time.Time) error {
	if leaf.IsCA || !publicKeysEqual(leaf.PublicKey, &key.PublicKey) {
		return errors.New("FoxOS TLS leaf certificate is invalid")
	}
	if err := leaf.CheckSignatureFrom(ca); err != nil {
		return errors.New("FoxOS TLS leaf certificate is not signed by the local CA")
	}
	if err := leaf.VerifyHostname(hostname); err != nil {
		return fmt.Errorf("FoxOS TLS certificate does not cover %s", hostname)
	}
	if err := leaf.VerifyHostname(address.String()); err != nil {
		return fmt.Errorf("FoxOS TLS certificate does not cover %s", address)
	}
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return errors.New("FoxOS TLS leaf certificate is not currently valid")
	}
	return nil
}

func writePEM(path string, mode os.FileMode, blockType string, der []byte, replace bool) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tls-*.pem")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := pem.Encode(temporary, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return os.ErrExist
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func publicKeysEqual(left any, right *ecdsa.PublicKey) bool {
	publicKey, ok := left.(*ecdsa.PublicKey)
	return ok && publicKey.Equal(right)
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}
