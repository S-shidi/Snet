package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/curve25519"

	"snet/internal/protocol"
)

func generateWGKeypair() (priv, pub string, err error) {
	key := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(key); err != nil {
		return "", "", err
	}
	point, err := curve25519.X25519(key, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	// WireGuard accepts the standard base64 encoding for both fields.
	return base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(point), nil
}

// GenerateWireGuardConfig creates a standalone WireGuard client config whose
// peer is gatewayNodeID. The returned private key is never stored server-side;
// the public key/IP pair is added as a WireGuard-only node so the gateway (and
// peers) can route the exported client.
func (s *Store) GenerateWireGuardConfig(token, gatewayNodeID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ns, meID, err := s.nodeFromTokenLocked(token)
	if err != nil {
		return "", err
	}
	if err := s.requireOwnerLocked(ns, meID); err != nil {
		return "", err
	}
	gatewayNodeID = strings.TrimSpace(gatewayNodeID)
	if gatewayNodeID == "" {
		return "", fmt.Errorf("%w: 请选择网关节点", ErrGatewayUnavailable)
	}
	gateway := ns.nodes[gatewayNodeID]
	if gateway == nil && gatewayNodeID == s.hubNodeID {
		gateway, err = s.ensureHubLocked(ns)
		if err != nil {
			return "", err
		}
	}
	if gateway == nil {
		return "", fmt.Errorf("%w: 网关节点不存在", ErrGatewayUnavailable)
	}
	if gateway.ID == s.hubNodeID && !ns.n.UseHubNode {
		ns.n.UseHubNode = true
		if err := s.persistNetwork(ns); err != nil {
			return "", err
		}
	}
	endpoint, ok, reason := publicGatewayEndpoint(gateway)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrGatewayUnavailable, reason)
	}
	if gateway.PublicKey == "" {
		return "", fmt.Errorf("%w: 网关缺少 WireGuard 公钥", ErrGatewayUnavailable)
	}

	priv, pub, err := generateWGKeypair()
	if err != nil {
		return "", err
	}
	if len(ns.nodes) >= protocol.MaxNodes {
		return "", ErrNetworkFull
	}
	ip, err := ns.allocIP()
	if err != nil {
		return "", err
	}
	nodeID, err := randomString(8)
	if err != nil {
		return "", err
	}
	client := &protocol.Node{
		ID:         nodeID,
		NetworkID:  ns.n.ID,
		IP:         ip,
		PublicKey:  pub,
		DeviceID:   "wg-" + strings.ToLower(nodeID),
		Role:       "member",
		DeviceName: "WireGuard 客户端",
	}
	ns.nodes[nodeID] = client
	if err := s.upsertDeviceLocked(client.DeviceID, pub, "WireGuard 客户端"); err != nil {
		return "", err
	}
	if err := s.persistNetwork(ns); err != nil {
		return "", err
	}
	if err := s.persistNode(ns.n.ID, client); err != nil {
		return "", err
	}

	allowed := make([]string, 0, 1+len(ns.n.Routes))
	seen := map[string]bool{}
	addAllowed := func(cidr string) {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" || seen[cidr] {
			return
		}
		seen[cidr] = true
		allowed = append(allowed, cidr)
	}
	addAllowed(ns.n.Subnet)
	for _, rt := range s.activeRoutesLocked(ns) {
		addAllowed(rt.CIDR)
	}

	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString("PrivateKey = " + priv + "\n")
	b.WriteString("Address = " + ip + "/32\n")
	b.WriteString("\n[Peer]\n")
	b.WriteString("PublicKey = " + gateway.PublicKey + "\n")
	b.WriteString("Endpoint = " + endpoint + "\n")
	b.WriteString("AllowedIPs = " + strings.Join(allowed, ", ") + "\n")
	b.WriteString("PersistentKeepalive = 25\n")
	return b.String(), nil
}
