package client

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/S-shidi/snet/shared/constants"
	"github.com/S-shidi/snet/shared/protocol"
)

// defaultSubnet is assumed when a legacy coordination server does not report
// a subnet.
const defaultSubnet = "10.88.0.0/24"

// PendingJoin is a persisted join request awaiting the network owner's
// approval. It is removed once approved (network added) or denied.
type PendingJoin struct {
	PendingID string `json:"pendingId"`
	NetworkID string `json:"networkId"`
	Code      string `json:"code,omitempty"`
	Name      string `json:"name,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	Status    string `json:"status,omitempty"` // pending | denied
	Err       string `json:"err,omitempty"`
	// SnetServURL is the coordination server this pending join belongs to.
	// Empty on legacy records; migrated to the official default.
	SnetServURL string `json:"snetServUrl,omitempty"`
}

// NetworkCfg is the persisted per-network state of this daemon. A daemon can
// hold any number of networks; only Active ones are brought up on start.
type NetworkCfg struct {
	Name        string `json:"name,omitempty"`
	NodeID      string `json:"nodeId"`
	IP          string `json:"ip"`
	Token       string `json:"token"`
	PairingCode string `json:"pairingCode,omitempty"`
	Subnet      string `json:"subnet,omitempty"`
	Port        int    `json:"port,omitempty"`
	Active      bool   `json:"active"`
	Owner       bool   `json:"owner,omitempty"`
	// PublicIP is the last public (NAT-observed) IPv4 the daemon advertised
	// for this network; restored on restart so relay-observed peer lists can
	// immediately exclude our own mapping without awaiting a fresh probe.
	PublicIP string `json:"publicIP,omitempty"`
	JoinedAt string `json:"joinedAt,omitempty"`
	// SnetServURL is the coordination server this network is bound to.
	// Empty on legacy records; migrated to the official default.
	SnetServURL string `json:"snetServUrl,omitempty"`
	// SnetServID is the stable identity of that coordination server.
	SnetServID string `json:"snetServId,omitempty"`
}

// Config is the daemon configuration (v2, multi-network). PrivateKey is the
// single WireGuard identity of this device and DeviceID is its stable
// identity that survives reinstalls.
// WireGuardExport is a generated standalone WireGuard client config. It is
// persisted in the local daemon config only (never on the coordination
// server) so the owner can re-open the QR/private key later. The key is
// sensitive; the config file is already mode 0600.
type WireGuardExport struct {
	NodeID        string `json:"nodeId"`
	NetworkID     string `json:"networkId"`
	GatewayNodeID string `json:"gatewayNodeId,omitempty"`
	GatewayName   string `json:"gatewayName,omitempty"`
	Config        string `json:"config"`
	CreatedAt     string `json:"createdAt"`
}

type Config struct {
	// SchemaVersion is the persisted client config schema version.
	SchemaVersion int    `json:"schemaVersion,omitempty"`
	WireguardPort int    `json:"wireguardPort"`
	PrivateKey    string `json:"privateKey"`
	DeviceID      string `json:"deviceId,omitempty"`
	DeviceName    string `json:"deviceName,omitempty"`
	// RecoveryToken is the legacy single-server recovery token. It is kept
	// for compatibility; RecoveryTokens holds one token per SnetServ.
	RecoveryToken string `json:"recoveryToken,omitempty"`
	// RecoveryTokens maps a canonical SnetServ URL to its recovery token.
	RecoveryTokens map[string]string       `json:"recoveryTokens,omitempty"`
	Networks       map[string]*NetworkCfg  `json:"networks,omitempty"`     // key = network ID
	PendingJoins   map[string]*PendingJoin `json:"pendingJoins,omitempty"` // key = pending ID`
	// WireGuardExports stores generated WireGuard configs by "nid/nodeID".
	// The private key is deliberately local-only and never uploaded.
	WireGuardExports map[string]WireGuardExport `json:"wireGuardExports,omitempty"`

	// KeyRotationDays is the interval in days between automatic WireGuard key
	// rotations (0 disables rotation). LastKeyRotatedAt is the unix second of
	// the last rotation (or of daemon startup when unset) used to schedule the
	// next one; both persist so a restart cannot reset the clock.
	KeyRotationDays  int   `json:"keyRotationDays,omitempty"`
	LastKeyRotatedAt int64 `json:"lastKeyRotatedAt,omitempty"`

	// WebPasswordHash is the bcrypt hash of the web console login password.
	// Empty means no password is set (authentication disabled).
	WebPasswordHash string `json:"webPasswordHash,omitempty"`

	// NATMapping enables automatic inbound port mapping (UPnP-IGD /
	// NAT-PMP / PCP) for this device's WireGuard ports. nil means enabled:
	// the product is zero-config, and a device whose gateway cooperates gets
	// a stable direct endpoint with no user action. Set to false to opt out
	// (security-sensitive deployments).
	NATMapping *bool `json:"natMapping,omitempty"`

	// AllowRelay makes this device act as a relay gateway for the networks
	// it belongs to: it forwards traffic for the subnets the network owner
	// delegated to it. Default false — a device only becomes a gateway when
	// the user explicitly allows it.
	//
	// Enabling it also requires the local gateway setup (IP forwarding plus
	// NAT masquerading) to succeed; when it fails the daemon reports the
	// error instead of silently advertising a gateway that cannot forward.
	AllowRelay bool `json:"allowRelay,omitempty"`

	// UseRelay is retained for compatibility with older clients. New clients
	// use RouteEnabled to control each delegated CIDR independently.
	UseRelay *bool `json:"useRelay,omitempty"`

	// RouteEnabled stores per-route preferences keyed by "networkID|cidr".
	// A missing key means enabled, preserving the original all-routes-on
	// behavior when upgrading an existing config.
	RouteEnabled map[string]bool `json:"routeEnabled,omitempty"`
}

// RelayAllowed reports whether this device may act as a relay gateway.
func (c *Config) RelayAllowed() bool {
	return c != nil && c.AllowRelay
}

// RelayUsed reports whether this device honours the network's delegated
// relay routes. Disabled by default means "used"; only an explicit false
// turns it off.
func (c *Config) RelayUsed() bool {
	return c != nil && (c.UseRelay == nil || *c.UseRelay)
}

// RouteAllowed reports whether a delegated route is enabled locally.
func (c *Config) RouteAllowed(nid, cidr string) bool {
	if c == nil || c.RouteEnabled == nil {
		return true
	}
	enabled, ok := c.RouteEnabled[nid+"|"+cidr]
	return !ok || enabled
}

// SetRouteAllowed updates and returns whether the stored value changed.
func (c *Config) SetRouteAllowed(nid, cidr string, enabled bool) bool {
	if c == nil {
		return false
	}
	key := nid + "|" + cidr
	if c.RouteEnabled == nil {
		c.RouteEnabled = make(map[string]bool)
	}
	if old, ok := c.RouteEnabled[key]; ok && old == enabled {
		return false
	}
	c.RouteEnabled[key] = enabled
	return true
}

func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "virtual-net")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// migrateConfig fills per-record compatibility fields without reading or
// preserving any legacy global default server. It is idempotent.
func migrateConfig(c *Config) {
	if c == nil {
		return
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = 1
	}
	for _, nc := range c.Networks {
		if nc != nil && nc.SnetServURL == "" {
			nc.SnetServURL = constants.DefaultServer
		}
	}
	for _, pj := range c.PendingJoins {
		if pj != nil && pj.SnetServURL == "" {
			pj.SnetServURL = constants.DefaultServer
		}
	}
	if c.RecoveryToken != "" && len(c.RecoveryTokens) == 0 {
		c.RecoveryTokens = map[string]string{constants.DefaultServer: c.RecoveryToken}
	}
}

// LoadConfigAt loads config from an explicit path (used for multi-instance
// testing); empty path falls back to the user config dir. v1 single-network
// configs are migrated to the v2 multi-network schema in place.
func LoadConfigAt(path string) (*Config, error) {
	if path == "" {
		return LoadConfig()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c := &Config{WireguardPort: protocol.DefaultWGPort}
			migrateConfig(c)
			return c, nil
		}
		return nil, err
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	if hasLegacyGlobalServerFields(data) {
		// Ignore and remove the old global server fields on load; they must
		// never participate in routing again.
		_ = cfg.SaveAt(path)
	}
	return cfg, nil
}

func hasLegacyGlobalServerFields(data []byte) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	if _, ok := raw["serverAddr"]; ok {
		return true
	}
	_, ok := raw["defaultSnetServUrl"]
	return ok
}

func parseConfig(data []byte) (*Config, error) {
	// The legacy schema kept networkId/nodeId/ip/token/pairingCode at the top
	// level; unmarshal those too so v1 configs migrate cleanly.
	var raw struct {
		Config
		NetworkID   string `json:"networkId"`
		NodeID      string `json:"nodeId"`
		IP          string `json:"ip"`
		Token       string `json:"token"`
		PairingCode string `json:"pairingCode"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	c := raw.Config
	if c.WireguardPort == 0 {
		c.WireguardPort = protocol.DefaultWGPort
	}
	if c.Networks == nil && raw.NetworkID != "" && raw.Token != "" {
		c.Networks = map[string]*NetworkCfg{
			raw.NetworkID: {
				NodeID:      raw.NodeID,
				IP:          raw.IP,
				Token:       raw.Token,
				PairingCode: raw.PairingCode,
				Subnet:      defaultSubnet,
				Port:        c.WireguardPort,
				Active:      true,
			},
		}
	}
	migrateConfig(&c)
	return &c, nil
}

func LoadConfig() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadConfigAt(path)
}

func (c *Config) Save() error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return c.SaveAt(path)
}

// SaveAt persists the config to an explicit path, creating parent dirs.
func (c *Config) SaveAt(path string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
