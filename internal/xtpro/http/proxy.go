package http

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	subdomainLength = 6
	readTimeout     = 30 * time.Second
	writeTimeout    = 30 * time.Second
)

type ClientSession interface {
	GetID() string
	GetKey() string
	SendHTTPRequest(req *HTTPRequest) (*HTTPResponse, error)
}

type HTTPProxyServer struct {
	mu             sync.RWMutex
	subdomains     map[string]ClientSession
	httpServer     *http.Server
	certFile       string
	keyFile        string
	baseDomain     string
	httpPort       int
	landingDir     string
	binDir         string
	dashboardProxy *httputil.ReverseProxy
}

type HTTPRequest struct {
	ID      string
	Method  string
	Path    string
	Headers map[string]string
	Body    []byte
}

type HTTPResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}

func NewHTTPProxyServer(certFile, keyFile, baseDomain string, httpPort int) *HTTPProxyServer {
	landingDir := "../frontend/landing"
	if _, err := os.Stat("frontend/landing"); err == nil {
		landingDir = "frontend/landing"
	}

	binDir := "bin"
	if _, err := os.Stat("bin"); os.IsNotExist(err) {
		binDir = "."
	}

	return &HTTPProxyServer{
		subdomains: make(map[string]ClientSession),
		certFile:   certFile,
		keyFile:    keyFile,
		baseDomain: baseDomain,
		httpPort:   httpPort,
		landingDir: landingDir,
		binDir:     binDir,
	}
}

func GenerateSubdomain() (string, error) {
	bytes := make([]byte, subdomainLength/2+1)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	subdomain := hex.EncodeToString(bytes)[:subdomainLength]
	return subdomain, nil
}

func (p *HTTPProxyServer) RegisterClient(subdomain string, client ClientSession) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.subdomains[subdomain]; exists {
		return fmt.Errorf("subdomain %s already registered", subdomain)
	}
	p.subdomains[subdomain] = client
	log.Printf("[http] Registered subdomain: %s.%s for client %s", subdomain, p.baseDomain, client.GetID())
	return nil
}

func (p *HTTPProxyServer) UnregisterClient(subdomain string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if client, exists := p.subdomains[subdomain]; exists {
		delete(p.subdomains, subdomain)
		log.Printf("[http] Unregistered subdomain: %s.%s for client %s", subdomain, p.baseDomain, client.GetID())
	}
}

func (p *HTTPProxyServer) Start() error {
	handler := http.HandlerFunc(p.handleRequest)
	p.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", p.httpPort),
		Handler:      handler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}

	log.Printf("[http] Starting HTTPS proxy server on port %d", p.httpPort)
	log.Printf("[http] Base domain: *.%s", p.baseDomain)
	return p.httpServer.ListenAndServeTLS(p.certFile, p.keyFile)
}

func (p *HTTPProxyServer) Stop() error {
	if p.httpServer != nil {
		return p.httpServer.Close()
	}
	return nil
}

func (p *HTTPProxyServer) SetDashboardTarget(port int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	targetURL := fmt.Sprintf("http://localhost:%d", port)
	u, _ := url.Parse(targetURL)
	p.dashboardProxy = httputil.NewSingleHostReverseProxy(u)

	originalDirector := p.dashboardProxy.Director
	p.dashboardProxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = u.Host
	}
}

func (p *HTTPProxyServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	if p.isMainDomain(r.Host) {
		if strings.HasPrefix(r.URL.Path, "/dashboard") || strings.HasPrefix(r.URL.Path, "/api") {
			p.mu.RLock()
			proxy := p.dashboardProxy
			p.mu.RUnlock()
			if proxy != nil {
				proxy.ServeHTTP(w, r)
				return
			}
		}
		p.serveLandingPage(w, r)
		return
	}

	subdomain := p.extractSubdomain(r.Host)
	if subdomain == "" {
		http.Error(w, "Invalid subdomain", http.StatusBadRequest)
		return
	}

	p.mu.RLock()
	client, exists := p.subdomains[subdomain]
	p.mu.RUnlock()
	if !exists {
		http.Error(w, fmt.Sprintf("Tunnel not found for subdomain: %s", subdomain), http.StatusBadGateway)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[http] Failed to read request body: %v", err)
		http.Error(w, "Failed to read request", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	headers := make(map[string]string)
	for key, values := range r.Header {
		headers[key] = strings.Join(values, ", ")
	}

	tunnelReq := &HTTPRequest{
		ID:      generateRequestID(),
		Method:  r.Method,
		Path:    r.URL.String(),
		Headers: headers,
		Body:    body,
	}

	response, err := client.SendHTTPRequest(tunnelReq)
	if err != nil {
		log.Printf("[http] Failed to send request through tunnel: %v", err)
		http.Error(w, "Failed to forward request", http.StatusBadGateway)
		return
	}

	for key, value := range response.Headers {
		w.Header().Set(key, value)
	}
	w.WriteHeader(response.StatusCode)
	if _, err := w.Write(response.Body); err != nil {
		log.Printf("[http] Failed to write response: %v", err)
	}
}

func (p *HTTPProxyServer) isMainDomain(host string) bool {
	if idx := strings.Index(host, ":"); idx > 0 {
		host = host[:idx]
	}
	return host == p.baseDomain || host == "www."+p.baseDomain
}

func (p *HTTPProxyServer) serveLandingPage(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/dashboard") || strings.HasPrefix(r.URL.Path, "/api") {
		return
	}

	if strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") ||
		r.URL.Path == "/" || r.URL.Path == "/index.html" {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	}

	if strings.HasPrefix(r.URL.Path, "/downloads/") {
		filename := strings.TrimPrefix(r.URL.Path, "/downloads/")
		filePath := filepath.Join(p.binDir, filename)
		if _, err := os.Stat(filePath); err == nil {
			http.ServeFile(w, r, filePath)
			return
		}
		http.NotFound(w, r)
		return
	}

	switch r.URL.Path {
	case "/", "/index.html":
		http.ServeFile(w, r, filepath.Join(p.landingDir, "index.html"))
	case "/style.css":
		w.Header().Set("Content-Type", "text/css")
		http.ServeFile(w, r, filepath.Join(p.landingDir, "style.css"))
	case "/script.js":
		w.Header().Set("Content-Type", "application/javascript")
		http.ServeFile(w, r, filepath.Join(p.landingDir, "script.js"))
	default:
		filePath := filepath.Join(p.landingDir, r.URL.Path)
		if _, err := os.Stat(filePath); err == nil {
			if strings.HasSuffix(filePath, ".css") {
				w.Header().Set("Content-Type", "text/css")
			} else if strings.HasSuffix(filePath, ".js") {
				w.Header().Set("Content-Type", "application/javascript")
			}
			http.ServeFile(w, r, filePath)
		} else {
			http.NotFound(w, r)
		}
	}
}

func (p *HTTPProxyServer) extractSubdomain(host string) string {
	if idx := strings.Index(host, ":"); idx > 0 {
		host = host[:idx]
	}

	suffix := "." + p.baseDomain
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	subdomain := strings.TrimSuffix(host, suffix)
	for _, ch := range subdomain {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			return ""
		}
	}
	return subdomain
}

func generateRequestID() string {
	bytes := make([]byte, 8)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func (p *HTTPProxyServer) GetBaseDomain() string { return p.baseDomain }

