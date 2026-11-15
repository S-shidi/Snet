package server

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"snet/internal/protocol"
)

const (
	hubDeviceID = "snet-hub"
	hubName     = "官方中继 (Hub)"
)

// ErrGatewayUnavailable is returned when a WireGuard export cannot use the
// requested node as a public gateway.
var ErrGatewayUnavailable = errors.New("gateway unavailable")

// SetHubNode configures the official Hub node. The public key/endpoint are
// optional: when omitted, the server reuses an existing node with the same ID
// from any network.
func (s *Store) SetHubNode(id, publicKey, endpoint string) error {
	id = strings.TrimSpace(id)
	publicKey = strings.TrimSpace(publicKey)
	endpoint = strings.TrimSpace(endpoint)
	if publicKey != "" {
		if err := validatePublicKey(publicKey); err != nil {
			return err
		}
	}
	if endpoint != "" {
		if err := validateEndpoint(endpoint); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hubNodeID = id
	s.hubPublicKey = publicKey
	s.hubEndpoint = endpoint
	if id != "" {
		log.Printf("official hub configured: node=%s endpoint=%s", id, endpoint)
	}
	return nil
}

func (s *Store) hubNodeIDLocked() string { return s.hubNodeID }

// hubSourceLocked resolves the Hub public key/endpoint from configuration or
// from an already-existing node with the configured Hub ID.
func (s *Store) hubSourceLocked() (string, string) {
	pub, ep := s.hubPublicKey, s.hubEndpoint
	if pub != "" && ep != "" {
		return pub, ep
	}
	for _, ns := range s.networks {
		if n := ns.nodes[s.hubNodeID]; n != nil {
			if pub == "" {
				pub = n.PublicKey
			}
			if ep == "" {
				ep = n.Endpoint
			}
			break
		}
	}
	return pub, ep
}

func nodeIPInUse(ns *networkState, ip string) bool {
	for _, n := range ns.nodes {
		if n.IP == ip {
			return true
		}
	}
	return false
}

func (ns *networkState) hubPreferredIP() (string, error) {
	if ns.subnetBase != nil {
		ip := addIPOffset(ns.subnetBase, 254).String()
		if !nodeIPInUse(ns, ip) {
			return ip, nil
		}
	}
	return ns.allocIP()
}

// ensureHubLocked adds the configured official Hub to ns when missing. The
// caller must hold s.mu.
func (s *Store) ensureHubLocked(ns *networkState) (*protocol.Node, error) {
	if s.hubNodeID == "" {
		return nil, fmt.Errorf("%w: 未配置 SNET_HUB_NODE_ID", ErrGatewayUnavailable)
	}
	if n := ns.nodes[s.hubNodeID]; n != nil {
		if !ns.n.UseHubNode {
			ns.n.UseHubNode = true
			if err := s.persistNetwork(ns); err != nil {
				return nil, err
			}
		}
		return n, nil
	}
	if len(ns.nodes) >= protocol.MaxNodes {
		return nil, ErrNetworkFull
	}
	pub, ep := s.hubSourceLocked()
	if pub == "" {
		return nil, fmt.Errorf("%w: 官方 Hub 公钥未配置", ErrGatewayUnavailable)
	}
	ip, err := ns.hubPreferredIP()
	if err != nil {
		return nil, err
	}
	tok, err := randomToken()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	n := &protocol.Node{
		ID:         s.hubNodeID,
		NetworkID:  ns.n.ID,
		IP:         ip,
		PublicKey:  pub,
		Endpoint:   ep,
		DeviceID:   hubDeviceID,
		LastSeen:   now,
		Role:       "member",
		AllowRelay: true,
	}
	ns.nodes[n.ID] = n
	s.byToken[hashToken(tok)] = tokenEntry{NetworkID: ns.n.ID, NodeID: n.ID}
	ns.n.UseHubNode = true
	if err := s.upsertDeviceLocked(hubDeviceID, pub, hubName); err != nil {
		return nil, err
	}
	if err := s.persistNetwork(ns); err != nil {
		return nil, err
	}
	if err := s.persistNode(ns.n.ID, n); err != nil {
		return nil, err
	}
	if err := s.persistToken(tok, tokenEntry{NetworkID: ns.n.ID, NodeID: n.ID}); err != nil {
		return nil, err
	}
	log.Printf("官方 Hub 已加入网络 %s (node=%s ip=%s)", ns.n.ID, n.ID, n.IP)
	return n, nil
}

// isPublicGatewayIP reports whether ip is a globally routable address. It
// excludes private, loopback, link-local, multicast, unspecified and CGNAT
// ranges.
func isPublicGatewayIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// RFC 6598 carrier-grade NAT.
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
	}
	return true
}

// publicGatewayEndpoint picks a usable public ip:port from the node's
// self-advertised endpoint or its relay-observed flow.
func publicGatewayEndpoint(n *protocol.Node) (string, bool, string) {
	if n == nil {
		return "", false, "节点不存在"
	}
	for _, ep := range []string{n.Endpoint, n.RelayFlow} {
		if ep == "" {
			continue
		}
		host, port, err := net.SplitHostPort(ep)
		if err != nil || host == "" || port == "" {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil || !isPublicGatewayIP(ip) {
			continue
		}
		if p, err := strconv.Atoi(port); err == nil && p > 0 && p <= 65535 {
			return ep, true, ""
		}
	}
	return "", false, "该节点处于内网 NAT 后，无公网端口"
}

// Gateways returns the owner's gateway candidates, including an unjoined Hub
// placeholder when the official Hub is configured.
func (s *Store) Gateways(token string) ([]protocol.GatewayInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return nil, err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	out := make([]protocol.GatewayInfo, 0, len(ns.nodes)+1)
	hubJoined := false
	for id, n := range ns.nodes {
		if id == s.hubNodeID {
			hubJoined = true
		}
		name := n.DeviceName
		if d := s.devices[n.DeviceID]; d != nil && d.Name != "" {
			name = d.Name
		}
		if name == "" {
			name = id
		}
		if id == s.hubNodeID {
			name = hubName
		}
		_, can, reason := publicGatewayEndpoint(n)
		if id == s.hubNodeID {
			// The official Hub is an operator-managed public relay. Treat it
			// as capable whenever its public key and endpoint are configured,
			// even if the SCM/daemon has not checked in recently.
			if n.PublicKey != "" && n.Endpoint != "" {
				can, reason = true, ""
			} else {
				can, reason = false, "官方 Hub 尚未配置公网入口"
			}
		}
		out = append(out, protocol.GatewayInfo{
			ID:           id,
			Name:         name,
			CanBeGateway: can,
			Reason:       reason,
			Joined:       true,
			Online:       id == s.hubNodeID || now-n.LastSeen < int64(lastSeenTTL/time.Second),
			IsHub:        id == s.hubNodeID,
		})
	}
	if s.hubNodeID != "" && !hubJoined {
		pub, ep := s.hubSourceLocked()
		can := pub != "" && ep != ""
		reason := ""
		if !can {
			reason = "官方 Hub 尚未配置公钥或公网入口"
		}
		out = append(out, protocol.GatewayInfo{
			ID:           s.hubNodeID,
			Name:         hubName,
			CanBeGateway: can,
			Reason:       reason,
			Joined:       false,
			IsHub:        true,
		})
	}
	return out, nil
}
