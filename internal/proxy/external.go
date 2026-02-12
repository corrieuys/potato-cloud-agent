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

// ExternalProxy routes incoming HTTP requests to services based on Host header.
type ExternalProxy struct {
	port     int
	bindAddr string
	routes   map[string]int // hostname -> port
	server   *http.Server
	mu       sync.RWMutex
}

// NewExternalProxy creates a new external reverse proxy.
func NewExternalProxy(port int, bindAddr string) *ExternalProxy {
	return &ExternalProxy{
		port:     port,
		bindAddr: bindAddr,
		routes:   make(map[string]int),
	}
}

// UpdateRoutes updates the routing table (hostname -> port).
func (p *ExternalProxy) UpdateRoutes(routes map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	next := make(map[string]int, len(routes))
	for k, v := range routes {
		next[k] = v
	}
	p.routes = next
}

// Start starts the proxy server.
func (p *ExternalProxy) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleRequest)

	p.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", p.bindAddr, p.port),
		Handler: mux,
	}

	return p.server.ListenAndServe()
}

// Stop stops the proxy server.
func (p *ExternalProxy) Stop() error {
	if p.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return p.server.Shutdown(ctx)
}

func (p *ExternalProxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Extract hostname from Host header (remove port if present)
	host := r.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	p.mu.RLock()
	port, exists := p.routes[host]
	p.mu.RUnlock()

	if !exists {
		http.Error(w, "No route found for hostname: "+host, http.StatusNotFound)
		return
	}

	targetURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		http.Error(w, "Invalid target URL", http.StatusInternalServerError)
		return
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Set forwarding headers
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Forwarded-Proto", "http")
	r.Header.Set("X-Forwarded-For", r.RemoteAddr)

	// Full path is preserved (no path stripping)
	proxy.ServeHTTP(w, r)
}

// GetPort returns the proxy port.
func (p *ExternalProxy) GetPort() int {
	return p.port
}

// GetBindAddr returns the bind address.
func (p *ExternalProxy) GetBindAddr() string {
	return p.bindAddr
}

// GetRoutes returns a copy of the current routes.
func (p *ExternalProxy) GetRoutes() map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make(map[string]int, len(p.routes))
	for k, v := range p.routes {
		out[k] = v
	}
	return out
}
