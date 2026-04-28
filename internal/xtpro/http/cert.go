package http

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

type CertManager struct {
	certFile string
	keyFile  string
}

func NewCertManager() *CertManager { return &CertManager{} }

func (cm *CertManager) LoadCertificate() error {
	locations := []struct {
		cert string
		key  string
	}{
		{"wildcard.crt", "wildcard.key"},
		{"server.crt", "server.key"},
		{"cert.pem", "key.pem"},
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc.cert); err == nil {
			if _, err := os.Stat(loc.key); err == nil {
				if err := cm.validateCertificate(loc.cert, loc.key); err == nil {
					cm.certFile = loc.cert
					cm.keyFile = loc.key
					log.Printf("[cert] Loaded certificate from: %s", loc.cert)
					return nil
				}
			}
		}
	}
	return fmt.Errorf("no valid SSL certificate found")
}

func (cm *CertManager) validateCertificate(certFile, keyFile string) error {
	_, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("failed to load certificate: %w", err)
	}
	return nil
}

func (cm *CertManager) GetCertFiles() (string, string, error) {
	if cm.certFile == "" || cm.keyFile == "" {
		return "", "", fmt.Errorf("certificate not loaded")
	}
	return cm.certFile, cm.keyFile, nil
}

func (cm *CertManager) GetCertificateInfo() string {
	if cm.certFile == "" {
		return "No certificate loaded"
	}
	return fmt.Sprintf("Certificate: %s, Key: %s",
		filepath.Base(cm.certFile),
		filepath.Base(cm.keyFile),
	)
}

