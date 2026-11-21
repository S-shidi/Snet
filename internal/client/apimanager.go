package client

import (
	"context"
	"strings"
	"sync"
)

// APIManager owns one reusable API client per coordination server. Callers
// that know a network's server must pass it explicitly; network routing never
// resolves its server through this type.
type APIManager struct {
	ctx     context.Context
	mu      sync.Mutex
	clients map[string]*apiClient
}

func NewAPIManager(ctx context.Context) *APIManager {
	if ctx == nil {
		ctx = context.Background()
	}
	return &APIManager{
		ctx:     ctx,
		clients: make(map[string]*apiClient),
	}
}

// Get returns the cached client for serverURL, creating it on first use.
// Server URLs are normalized so trailing slashes and scheme-less hosts share
// the same client. An empty server returns nil; callers must supply a server.
func (m *APIManager) Get(serverURL string) *apiClient {
	if m == nil {
		return nil
	}
	serverURL = normalizeServer(strings.TrimSpace(serverURL))
	if serverURL == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if client := m.clients[serverURL]; client != nil {
		return client
	}
	client := newAPIClient(serverURL, m.ctx)
	m.clients[serverURL] = client
	return client
}
