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

// ExternalProxy routes incoming HTTP requests to services
type ExternalProxy struct {
	port     int
	bindAddr string
	routes   map[string]int // path -> port
	server   *http.Server
	mu       sync.RWMutex
}

// NewExternalProxy creates a new external reverse proxy
func NewExternalProxy(port int, bindAddr string) *ExternalProxy {
	return &ExternalProxy{
		port:     port,
		bindAddr: bindAddr,
		routes:   make(map[string]int),
	}
}

// UpdateRoutes updates the routing table
func (p *ExternalProxy) UpdateRoutes(routes map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routes = routes
}

// Start starts the proxy server
func (p *ExternalProxy) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.handleRequest)

	p.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", p.bindAddr, p.port),
		Handler: mux,
	}

	return p.server.ListenAndServe()
}

// Stop stops the proxy server
func (p *ExternalProxy) Stop() error {
	if p.server == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return p.server.Shutdown(ctx)
}

// handleRequest routes requests to the appropriate service
func (p *ExternalProxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	routes := make(map[string]int)
	for k, v := range p.routes {
		routes[k] = v
	}
	p.mu.RUnlock()

	// Find matching route (longest prefix match)
	var matchedPort int
	var matchedPath string

	for path, port := range routes {
		if strings.HasPrefix(r.URL.Path, path) {
			if len(path) > len(matchedPath) {
				matchedPath = path
				matchedPort = port
			}
		}
	}

	if matchedPort == 0 {
		http.Error(w, "No route found", http.StatusNotFound)
		return
	}

	// Use simple reverse proxy
	targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", matchedPort))
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Update the request URL
	r.URL.Path = strings.TrimPrefix(r.URL.Path, matchedPath)
	if !strings.HasPrefix(r.URL.Path, "/") {
		r.URL.Path = "/" + r.URL.Path
	}

	// Add X-Forwarded headers
	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Forwarded-Proto", "http")
	r.Header.Set("X-Forwarded-For", r.RemoteAddr)

	proxy.ServeHTTP(w, r)
}

// GetPort returns the proxy port
func (p *ExternalProxy) GetPort() int {
	return p.port
}

// GetBindAddr returns the bind address
func (p *ExternalProxy) GetBindAddr() string {
	return p.bindAddr
}
