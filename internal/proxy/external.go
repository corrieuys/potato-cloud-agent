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

// ExternalProxy routes incoming HTTP requests to services.
type ExternalProxy struct {
	port        int
	bindAddr    string
	routes      map[string]int
	stackRoutes map[string]map[string]int
	server      *http.Server
	mu          sync.RWMutex
}

// NewExternalProxy creates a new external reverse proxy.
func NewExternalProxy(port int, bindAddr string) *ExternalProxy {
	return &ExternalProxy{
		port:        port,
		bindAddr:    bindAddr,
		routes:      make(map[string]int),
		stackRoutes: make(map[string]map[string]int),
	}
}

// UpdateRoutes updates the global routing table.
func (p *ExternalProxy) UpdateRoutes(routes map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	next := make(map[string]int, len(routes))
	for k, v := range routes {
		next[k] = v
	}
	p.routes = next
}

// UpdateStackRoutes updates stack-specific routes.
func (p *ExternalProxy) UpdateStackRoutes(stackID string, routes map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	next := make(map[string]int, len(routes))
	for k, v := range routes {
		next[k] = v
	}
	p.stackRoutes[stackID] = next
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
	stackID := extractStackID(r.Host)

	p.mu.RLock()
	matchedPath, matchedPort, useStackHost := longestPrefixMatch(r.URL.Path, p.routes, nil)
	if stackID != "" {
		if stackSpecific, ok := p.stackRoutes[stackID]; ok {
			stackPath, stackPort, _ := longestPrefixMatch(r.URL.Path, nil, stackSpecific)
			if len(stackPath) > len(matchedPath) {
				matchedPath = stackPath
				matchedPort = stackPort
				useStackHost = true
			}
		}
	}
	p.mu.RUnlock()

	if matchedPort == 0 {
		http.Error(w, "No route found", http.StatusNotFound)
		return
	}

	targetHost := "127.0.0.1"
	if useStackHost && stackID != "" {
		targetHost = fmt.Sprintf("stack-%s.svc.internal", stackID)
	}

	targetURL, err := url.Parse(fmt.Sprintf("http://%s:%d", targetHost, matchedPort))
	if err != nil {
		http.Error(w, "Invalid target URL", http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	r.URL.Path = strings.TrimPrefix(r.URL.Path, matchedPath)
	if !strings.HasPrefix(r.URL.Path, "/") {
		r.URL.Path = "/" + r.URL.Path
	}

	r.Header.Set("X-Forwarded-Host", r.Host)
	r.Header.Set("X-Forwarded-Proto", "http")
	r.Header.Set("X-Forwarded-For", r.RemoteAddr)

	proxy.ServeHTTP(w, r)
}

func longestPrefixMatch(path string, global map[string]int, stack map[string]int) (string, int, bool) {
	var matchedPath string
	var matchedPort int
	useStackHost := false

	for route, port := range global {
		if strings.HasPrefix(path, route) && len(route) > len(matchedPath) {
			matchedPath = route
			matchedPort = port
			useStackHost = false
		}
	}
	for route, port := range stack {
		if strings.HasPrefix(path, route) && len(route) > len(matchedPath) {
			matchedPath = route
			matchedPort = port
			useStackHost = true
		}
	}

	return matchedPath, matchedPort, useStackHost
}

func extractStackID(host string) string {
	name := host
	if idx := strings.Index(name, ":"); idx != -1 {
		name = name[:idx]
	}

	hostParts := strings.Split(name, ".")
	if len(hostParts) == 0 {
		return ""
	}
	if !strings.HasPrefix(hostParts[0], "stack-") {
		return ""
	}
	return strings.TrimPrefix(hostParts[0], "stack-")
}

// AddStackRoute adds a route for a specific stack.
func (p *ExternalProxy) AddStackRoute(stackID, path string, port int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.stackRoutes[stackID]; !exists {
		p.stackRoutes[stackID] = make(map[string]int)
	}
	p.stackRoutes[stackID][path] = port
}

// RemoveStackRoute removes a route for a specific stack.
func (p *ExternalProxy) RemoveStackRoute(stackID, path string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if routes, exists := p.stackRoutes[stackID]; exists {
		delete(routes, path)
		if len(routes) == 0 {
			delete(p.stackRoutes, stackID)
		}
	}
}

// ClearStackRoutes clears all routes for a specific stack.
func (p *ExternalProxy) ClearStackRoutes(stackID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.stackRoutes, stackID)
}

// GetStackRoutes returns all routes for a specific stack.
func (p *ExternalProxy) GetStackRoutes(stackID string) map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	src, exists := p.stackRoutes[stackID]
	if !exists {
		return nil
	}
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// GetAllStackRoutes returns all stack routes.
func (p *ExternalProxy) GetAllStackRoutes() map[string]map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make(map[string]map[string]int, len(p.stackRoutes))
	for stackID, routes := range p.stackRoutes {
		out[stackID] = make(map[string]int, len(routes))
		for path, port := range routes {
			out[stackID][path] = port
		}
	}
	return out
}

// HasStackRoutes checks if a stack has any routes.
func (p *ExternalProxy) HasStackRoutes(stackID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, exists := p.stackRoutes[stackID]
	return exists
}

// GetPort returns the proxy port.
func (p *ExternalProxy) GetPort() int {
	return p.port
}

// GetBindAddr returns the bind address.
func (p *ExternalProxy) GetBindAddr() string {
	return p.bindAddr
}
