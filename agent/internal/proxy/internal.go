package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// InternalProxy routes requests by Host header for service-to-service communication
type InternalProxy struct {
	routes map[string]int // service name -> port
	server *http.Server
	mu     sync.RWMutex
}

// NewInternalProxy creates a new internal reverse proxy
func NewInternalProxy() *InternalProxy {
	return &InternalProxy{
		routes: make(map[string]int),
	}
}

// UpdateRoutes updates the internal routing table
func (p *InternalProxy) UpdateRoutes(routes map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes = routes
}

// Start starts the internal proxy server on port 80
func (p *InternalProxy) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleRequest)

	p.server = &http.Server{
		Addr:    "127.0.0.1:80",
		Handler: mux,
	}

	return p.server.ListenAndServe()
}

// Stop stops the internal proxy server
func (p *InternalProxy) Stop() error {
	if p.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return p.server.Shutdown(ctx)
}

// handleRequest routes requests based on Host header
func (p *InternalProxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	host := r.Host

	// Strip port if present
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Check if it's a svc.internal domain
	if !strings.HasSuffix(host, ".svc.internal") {
		http.Error(w, "Invalid host", http.StatusBadRequest)
		return
	}

	// Extract service name
	serviceName := strings.TrimSuffix(host, ".svc.internal")

	p.mu.RLock()
	port, exists := p.routes[serviceName]
	p.mu.RUnlock()

	if !exists {
		http.Error(w, fmt.Sprintf("Service '%s' not found", serviceName), http.StatusNotFound)
		return
	}

	// Proxy the request
	targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Add X-Forwarded headers
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Forwarded-Proto", "http")
	r.Header.Set("X-Forwarded-For", r.RemoteAddr)

	proxy.ServeHTTP(w, r)
}

// GetServiceURL returns the internal URL for a service
func (p *InternalProxy) GetServiceURL(serviceName string) string {
	return fmt.Sprintf("http://%s.svc.internal", serviceName)
}
