package controller

import (
	"errors"
	"net"
	"net/http"
	"sync"
)

type Manager struct {
	mu   sync.RWMutex
	byID map[string]*Server
	byIP map[string]string
}

func NewManager() *Manager {
	return &Manager{byID: make(map[string]*Server), byIP: make(map[string]string)}
}

func (m *Manager) Register(id, address string, server *Server) error {
	ip := net.ParseIP(address)
	if id == "" || ip == nil || server == nil || server.auth.ID != id || !server.allowedIP.Equal(ip) {
		return errors.New("invalid AP registration")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if owner, exists := m.byIP[ip.String()]; exists && owner != id {
		return errors.New("AP management address already registered")
	}
	if previous, exists := m.byID[id]; exists {
		delete(m.byIP, previous.allowedIP.String())
		previous.Disconnect()
	}
	m.byID[id] = server
	m.byIP[ip.String()] = id
	return nil
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	previous := m.byID[id]
	if previous != nil {
		delete(m.byID, id)
		delete(m.byIP, previous.allowedIP.String())
	}
	m.mu.Unlock()
	if previous != nil {
		previous.Disconnect()
	}
}

func (m *Manager) Server(id string) (*Server, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	server, ok := m.byID[id]
	return server, ok
}

func (m *Manager) Servers() map[string]*Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*Server, len(m.byID))
	for id, server := range m.byID {
		result[id] = server
	}
	return result
}

func (m *Manager) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		http.Error(writer, "AP not registered", http.StatusForbidden)
		return
	}
	ip := net.ParseIP(host)
	if ip == nil {
		http.Error(writer, "AP not registered", http.StatusForbidden)
		return
	}
	m.mu.RLock()
	id := m.byIP[ip.String()]
	server := m.byID[id]
	m.mu.RUnlock()
	if server == nil {
		http.Error(writer, "AP not registered", http.StatusForbidden)
		return
	}
	server.ServeHTTP(writer, request)
}
