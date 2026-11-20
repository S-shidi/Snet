package client

import (
	"context"
	"strings"
	"sync"

	"snet/internal/constants"
)

// APIManager owns one reusable API client per coordination server. Stage 0
// only prepares this routing base; existing callers still select the server
// through Daemon.serverAddrLocked().
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
// the same client.
func (m *APIManager) Get(serverURL string) *apiClient {
	if m == nil {
		return nil
	}
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		serverURL = constants.DefaultServer
	}
	serverURL = normalizeServer(serverURL)

	m.mu.Lock()
	defer m.mu.Unlock()
	if client := m.clients[serverURL]; client != nil {
		return client
	}
	client := newAPIClient(serverURL, m.ctx)
	m.clients[serverURL] = client
	return client
}
