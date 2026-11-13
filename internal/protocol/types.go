package protocol

import (
	"time"
)

const (
	DefaultMTU            = 1320
	DefaultWGPort         = 51820
	MaxNodes              = 100
	KeepaliveInterval     = 10
	KeepaliveIntervalIdle = 25 // Longer interval when idle (mobile power saving)
	PollIntervalSeconds   = 2
	ProbeIntervalSeconds  = 15
	ProbePort             = 8091
)

// APIVersion is the coordination API major version. Clients send it on every
// request via VersionHeader; servers advertise it on every response. A major
// mismatch is rejectable server-side (426) and surfaced client-side as a
// compatibility warning, so protocol changes never fail silently.
const APIVersion = 1

// VersionHeader names the request/response header carrying the API major
// version. Absent header (legacy clients/servers) means "no enforcement".
const VersionHeader = "X-Snet-Api-Version"

// RelayCtrlPrefix leads each control packet a client sends to a network's
// relay port. WireGuard messages start with a type byte of 1..4, so this
// prefix cannot collide with real tunnel traffic. Old relays merely fan the
// packet out to other peers, where it is dropped; the protocol is backwards
// compatible.
const RelayCtrlPrefix = "\xfeSNET1"

// Relay control operation names.
const (
	RelayCtrlWhoami = "whoami" // ask the relay for the observed source endpoint
	RelayCtrlGroup  = "group"  // ask the relay for all live peer endpoints
)

type Network struct {
	ID               string `json:"id"`
	PairingCode      string `json:"pairingCode"`
	OwnerNodeID      string `json:"ownerNodeId"`
	CreatedAt        string `json:"createdAt"`
	Name             string `json:"name,omitempty"`
	Subnet           string `json:"subnet,omitempty"`
	OwnerDeviceID    string `json:"ownerDeviceId,omitempty"`
	LastActivityAt   int64  `json:"lastActivityAt,omitempty"`
	RelayPort        int    `json:"relayPort,omitempty"`
	NodeCount        int    `json:"nodeCount,omitempty"`
	Online           bool   `json:"online,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
	PendingCount     int    `json:"pendingCount,omitempty"`
	// Managed 标记服务端直接创建的网络:无网主、不受僵尸清理、客户端不可认领。
	Managed bool `json:"managed,omitempty"`
	// Description 是网络的简短描述（分享时展示给潜在加入者）。
	Description string `json:"description,omitempty"`
	// Tags 是网络的分类标签。
	Tags []string `json:"tags,omitempty"`
	// Visibility 标记网络的公开性："shareable" 表示可在社区目录中被发现。
	Visibility string `json:"visibility,omitempty"`
	// Routes 是网络所有者委派出去的中继路由（双向确认）。
	// 每条路由必须由目标节点确认开启 allow_relay 后才会生效。
	Routes []Route `json:"routes,omitempty"`
}

// Route 是一条由网络所有者委派的中继路由：目标节点（TargetNodeID）被要求
// 充当该 CIDR 的网关，其他成员把到该 CIDR 的流量经由目标节点转发。
//
// 状态机：
//   - pending：所有者已委派，但目标节点尚未开启 allow_relay（或网关配置失败）。
//   - active：目标节点已上报 allow_relay，路由生效并下发全网络。
type Route struct {
	// TargetNodeID 是承担网关角色的节点 ID。
	TargetNodeID string `json:"targetNodeId"`
	// CIDR 是被委派给目标节点的网段，例如 "192.168.3.0/24"。
	CIDR string `json:"cidr"`
	// Status 是 "pending" 或 "active"。
	Status string `json:"status"`
}

// 中继路由状态常量。
const (
	RouteStatusPending = "pending"
	RouteStatusActive  = "active"
)

type Node struct {
	ID        string `json:"id"`
	NetworkID string `json:"networkId"`
	IP        string `json:"ip"`
	// IPv6 is the virtual IPv6 address derived from the IPv4 address and subnet.
	// For example, 10.88.1.5 in subnet 10.88.1.0/24 becomes fd00:a:58:1::5.
	IPv6      string `json:"ipv6,omitempty"`
	PublicKey string `json:"publicKey"`
	Endpoint  string `json:"endpoint"`
	// EndpointV6 is the node's global-IPv6 direct endpoint ([v6]:port). IPv6
	// has no NAT, so peers with a global v6 address can reach each other
	// directly without hole punching or port forwarding.
	EndpointV6 string `json:"endpointV6,omitempty"`
	// LocalEndpoint is the node's LAN/NAT-internal address candidate.
	// Nodes on the same subnet prefer this over the relay endpoint.
	LocalEndpoint string `json:"localEndpoint,omitempty"`
	// DirectEndpoint is the Relay v2 peer-to-peer socket's LAN address. It is
	// distinct from LocalEndpoint because Relay v2 uses a dedicated UDP socket
	// rather than the WireGuard listen socket.
	DirectEndpoint string `json:"directEndpoint,omitempty"`
	// DirectEndpointV6 is the Relay v2 peer-to-peer socket's global IPv6
	// address ([v6]:port), used before public IPv4 hole punching.
	DirectEndpointV6 string `json:"directEndpointV6,omitempty"`
	// NATType is this node's self-classified NAT behaviour: "open", "cone",
	// "symmetric" or "unknown". It is advisory only; relay remains the
	// fallback when a direct path cannot be established.
	NATType string `json:"natType,omitempty"`
	// EndpointMapped reports whether Endpoint is a stable inbound port
	// mapping obtained from the network gateway (UPnP-IGD / NAT-PMP / PCP)
	// rather than a relay-observed transient NAT pinhole. Peers treat a
	// mapped endpoint as a primary direct target even while RelayFlow is
	// non-empty (a node behind an endpoint-dependent NAT cannot otherwise be
	// reached directly), so an automatic mapping is the only zero-config
	// path to a stable hole for symmetric-NAT peers.
	EndpointMapped bool   `json:"endpointMapped,omitempty"`
	LastSeen       int64  `json:"lastSeen,omitempty"`
	DeviceID       string `json:"deviceId,omitempty"`
	// DeviceName is the display name of the bound device (admin views only;
	// empty when the device never reported a name).
	DeviceName string `json:"deviceName,omitempty"`
	// RelayFlow is this node's most recent live NAT mapping observed on the
	// network relay ("ip:port"). It is the exact address a peer should punch
	// toward for a direct connection, and is attributed per node by the
	// server from the relay's observed flows (matching the node's control
	// plane public IP). Empty when the node has not contacted the relay yet.
	RelayFlow string `json:"relayFlow,omitempty"`
	// RelayPort is the per-node unicast relay port assigned to this node.
	// Peers send relay traffic to relayHost:RelayPort instead of the shared
	// broadcast port, enabling unicast routing without fan-out amplification.
	// Zero means the node still uses the legacy broadcast port.
	RelayPort int `json:"relayPort,omitempty"`
	// RelayHostOverride is the relay instance host that serves this node's
	// per-node unicast port. In a multi-instance topology (relay alternates)
	// different nodes of one network may be assigned different relay
	// instances; peers must send this node's relay traffic to
	// RelayHostOverride:RelayPort rather than the network's primary relay.
	// Empty means the node is served by the network's primary relay host.
	RelayHostOverride string `json:"relayHostOverride,omitempty"`
	Online            bool   `json:"online,omitempty"`
	// AllowedIPs is the client-computed set of effective CIDRs for this
	// peer: the WireGuard AllowedIPs and OS routes derived from the network's
	// active delegated Routes. The server never populates or sends this field.
	AllowedIPs []string `json:"allowedIPs,omitempty"`
	// Role 是节点在网络中的角色："owner"（创建者）| "member"（普通成员）。
	Role string `json:"role,omitempty"`
	// PublicIPv4 is the node's public IPv4 address (without port), extracted
	// from RelayFlow or Endpoint. Shown in the admin console for diagnostics.
	PublicIPv4 string `json:"publicIPv4,omitempty"`
	// PublicIPv6 is the node's public IPv6 address (without port), extracted
	// from EndpointV6. Shown in the admin console for diagnostics.
	PublicIPv6 string `json:"publicIPv6,omitempty"`
	// AllowRelay 为 true 表示该节点已开启"允许作为中继网关"，并已成功完成
	// 网关配置（IP 转发 + NAT）。只有上报 true 的节点才能让委派给它的
	// 中继路由从 pending 变为 active。
	AllowRelay bool `json:"allowRelay,omitempty"`
	// RelayError 描述网关配置失败的原因（例如缺少 iptables 权限）。
	// 非空表示节点虽然想当网关但实际不可用，路由会一直保持 pending。
	RelayError string `json:"relayError,omitempty"`
}

type CreateNetworkReq struct {
	PublicKey        string   `json:"publicKey"`
	DeviceID         string   `json:"deviceId,omitempty"`
	Name             string   `json:"name,omitempty"`
	Subnet           string   `json:"subnet,omitempty"`
	ApprovalRequired bool     `json:"approvalRequired,omitempty"`
	Description      string   `json:"description,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Visibility       string   `json:"visibility,omitempty"`
	// AllowOverlap permits an explicitly chosen subnet that overlaps an
	// existing network. Default false: overlapping networks are rejected.
	AllowOverlap bool `json:"allowOverlap,omitempty"`
}

type CreateNetworkResp struct {
	NetworkID        string `json:"networkId"`
	PairingCode      string `json:"pairingCode"`
	NodeID           string `json:"nodeId"`
	IP               string `json:"ip"`
	Token            string `json:"token"`
	Subnet           string `json:"subnet,omitempty"`
	RelayPort        int    `json:"relayPort,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
}

type JoinReq struct {
	Code      string `json:"code"`
	PublicKey string `json:"publicKey"`
	DeviceID  string `json:"deviceId,omitempty"`
}

type JoinResp struct {
	NetworkID string `json:"networkId"`
	Name      string `json:"name,omitempty"`
	NodeID    string `json:"nodeId"`
	IP        string `json:"ip"`
	Token     string `json:"token"`
	Subnet    string `json:"subnet,omitempty"`
	RelayPort int    `json:"relayPort,omitempty"`
	Peers     []Node `json:"peers"`
	// Status 为 "pending" 时表示加入需网络创建者批准：不返回 IP/Token，
	// 客户端应轮询 PendingID 对应的待批准状态。空/“ok” 为正常加入。
	Status    string `json:"status,omitempty"`
	PendingID string `json:"pendingId,omitempty"`
}

type SetEndpointReq struct {
	Endpoint string `json:"endpoint"`
	// LocalEndpoint is the node's LAN/NAT-internal address candidate
	// (host:port). Peers on the same subnet may connect to it directly to
	// save relay bandwidth.
	LocalEndpoint string `json:"localEndpoint,omitempty"`
	// EndpointV6 is the node's global-IPv6 endpoint ([v6]:port), advertised
	// when the device has a routable global v6 address.
	EndpointV6 string `json:"endpointV6,omitempty"`
	// EndpointMapped reports whether Endpoint is a stable inbound gateway
	// mapping; peers prioritize it as a direct target even with a live relay.
	EndpointMapped bool `json:"endpointMapped,omitempty"`
}

// RelayV2Info tells clients where the session relay listens. The relay
// itself is a separate service that only verifies control-plane tickets and
// forwards opaque WireGuard ciphertext.
type RelayV2Info struct {
	Enabled bool `json:"enabled"`
	// Endpoint is the primary Relay v2 UDP endpoint.
	Endpoint string `json:"endpoint,omitempty"`
	// Endpoints lists every Relay v2 instance sharing the same ticket secret.
	// Clients keep the primary while healthy and fail over to the next entry.
	Endpoints []string `json:"endpoints,omitempty"`
}

// RelayTicketResp is issued per network/node. SessionID identifies the
// client's relay session; NodeKey is the compact routing identity used in
// Relay v2 frames.
type RelayTicketResp struct {
	Endpoint  string   `json:"endpoint"`
	Endpoints []string `json:"endpoints,omitempty"`
	Ticket    string   `json:"ticket"`
	SessionID string   `json:"sessionId"`
	NodeID    string   `json:"nodeId"`
	NodeKey   string   `json:"nodeKey"`
	ExpiresAt int64    `json:"expiresAt"`
}

// SetDirectEndpointsReq publishes the Relay v2 peer-to-peer socket addresses.
// Empty values clear the corresponding family; NATType is advisory.
type SetDirectEndpointsReq struct {
	DirectEndpoint   string `json:"directEndpoint,omitempty"`
	DirectEndpointV6 string `json:"directEndpointV6,omitempty"`
	NATType          string `json:"natType,omitempty"`
}

type PeersResp struct {
	Peers []Node `json:"peers"`
	Name  string `json:"name,omitempty"`
	// Self 是调用者自身的节点（含最新 IP，用于网段变更后自动重配）。
	Self             *Node  `json:"self,omitempty"`
	Subnet           string `json:"subnet,omitempty"`
	ApprovalRequired bool   `json:"approvalRequired,omitempty"`
	// RelayV2 describes the session-oriented relay that clients should use
	// for the relay-first data plane. Nil/disabled means legacy relay mode.
	RelayV2 *RelayV2Info `json:"relayV2,omitempty"`
	// RelayEndpoint is the relay address for the network. Peers use it as a
	// connectivity fallback when a direct (LAN/self-advertised) endpoint
	// cannot be reached. Empty when relay mode is disabled.
	RelayEndpoint string `json:"relayEndpoint,omitempty"`
	// RelayEndpoints lists every candidate relay for the network, with
	// RelayEndpoint (when non-empty) repeated first as the coordinated
	// primary. Clients measure latency to each candidate and pick the best
	// for their data plane; on failure they fall back to the next candidate
	// rather than dropping off the mesh. Empty when relay mode is disabled
	// or the server has a single relay (in which case RelayEndpoint alone is
	// set).
	RelayEndpoints []string `json:"relayEndpoints,omitempty"`
	// Routes 是本网络已生效（active）的中继路由。所有成员都会收到，用于把
	// 委派网段的流量注入到目标节点的隧道上。
	Routes []Route `json:"routes,omitempty"`
}

// ClaimReq claims ownership of a legacy network. Requires the calling device
// to already own a node inside the network.
type ClaimReq struct {
	DeviceID string `json:"deviceId"`
}

type SetNodeDeviceReq struct {
	DeviceID string `json:"deviceId"`
}

// NetworkSettingsReq updates a network's name, subnet and/or join-approval
// setting. Empty name/subnet keep the current value; a nil approval keeps it.
type NetworkSettingsReq struct {
	Name             string   `json:"name"`
	Subnet           string   `json:"subnet"`
	ApprovalRequired *bool    `json:"approvalRequired"`
	Description      *string  `json:"description,omitempty"`
	Tags             []string `json:"tags"`
	Visibility       *string  `json:"visibility,omitempty"`
}

// SetRoutesReq replaces the network's delegated relay routes. Only the
// network owner may call it. Every route is stored as "pending"; it becomes
// "active" once the target node reports allow_relay.
type SetRoutesReq struct {
	Routes []Route `json:"routes"`
}

// UpdateRelayCapabilityReq reports whether the calling node is willing and
// able to act as a relay gateway for the network. RelayError carries the
// reason the gateway setup failed; a non-empty error keeps the node's
// delegated routes pending.
type UpdateRelayCapabilityReq struct {
	AllowRelay bool   `json:"allowRelay"`
	RelayError string `json:"relayError,omitempty"`
}

type NetworkInfoResp struct {
	Network
	Nodes   []Node        `json:"nodes"`
	Pending []PendingNode `json:"pending,omitempty"`
}

type PendingNode struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	DeviceID  string `json:"deviceId,omitempty"`
	// DeviceName is the device's display name (empty when never reported).
	DeviceName string `json:"deviceName,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

// PendingStatusResp reports a pending join's state. Status 为 "pending" /
// "approved" / "denied"；approved 时附带完整的加入信息（IP/Token 等）。
type PendingStatusResp struct {
	Status    string `json:"status"`
	NetworkID string `json:"networkId,omitempty"`
	Name      string `json:"name,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
	IP        string `json:"ip,omitempty"`
	Token     string `json:"token,omitempty"`
	Subnet    string `json:"subnet,omitempty"`
	RelayPort int    `json:"relayPort,omitempty"`
	Peers     []Node `json:"peers,omitempty"`
}

type NetworksResp struct {
	Networks []Network `json:"networks"`
}

type ResetCodeResp struct {
	PairingCode string `json:"pairingCode"`
}

type Device struct {
	ID        string `json:"id"`
	PublicKey string `json:"publicKey"`
	CreatedAt string `json:"createdAt"`
	LastSeen  int64  `json:"lastSeen,omitempty"`
	Name      string `json:"name,omitempty"`
	// PublicIPv4 is the device's last known public IPv4 address (without port).
	PublicIPv4 string `json:"publicIPv4,omitempty"`
	// PublicIPv6 is the device's last known public IPv6 address (without port).
	PublicIPv6 string `json:"publicIPv6,omitempty"`
	// Networks is the list of network IDs this device belongs to.
	Networks []DeviceNetwork `json:"networks,omitempty"`
}

// DeviceNetwork represents a network membership for a device.
type DeviceNetwork struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type RegisterDeviceReq struct {
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
	Name      string `json:"name,omitempty"`
}

// AdminRenameDeviceReq updates a device's display name in the admin console.
type AdminRenameDeviceReq struct {
	Name string `json:"name"`
}

type RegisterDeviceResp struct {
	DeviceID string `json:"deviceId"`
}

// AdminCreateNetworkReq creates a server-managed network. Managed networks
// have no client owner, are exempt from zombie reaping and cannot be claimed.
type AdminCreateNetworkReq struct {
	Name             string   `json:"name,omitempty"`
	Subnet           string   `json:"subnet,omitempty"`
	ApprovalRequired bool     `json:"approvalRequired,omitempty"`
	Description      string   `json:"description,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Visibility       string   `json:"visibility,omitempty"`
	AllowOverlap     bool     `json:"allowOverlap,omitempty"`
}

type AdminCreateNetworkResp struct {
	NetworkID   string `json:"networkId"`
	PairingCode string `json:"pairingCode"`
	Subnet      string `json:"subnet,omitempty"`
	RelayPort   int    `json:"relayPort,omitempty"`
}

// AdminPasswordReq changes the admin login password. OldPassword is verified
// against the currently stored hash; on success all admin sessions are
// invalidated.
type AdminPasswordReq struct {
	OldPassword string `json:"oldPassword"`
	NewPassword string `json:"newPassword"`
}

// AdminLoginReq authenticates with username+password and returns a session
// token usable as an Authorization: Bearer <token> header.
type AdminLoginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AdminLoginResp struct {
	Token   string `json:"token"`
	Expires int64  `json:"expires"`
}

type ErrResp struct {
	Error string `json:"error"`
}

// AuthCodeLen is the length of a device authorization code. Codes use the
// 32-symbol idAlphabet (≈5 bits/symbol), so a 16-symbol code carries ≈80 bits
// of entropy. Only a hash of the code is ever stored.
const AuthCodeLen = 16

// BindDeviceReq binds a device to this server using a one-time authorization
// code generated by the administrator. The code is sent once over the control
// channel and never persisted by the client.
type BindDeviceReq struct {
	Code      string `json:"code"`
	DeviceID  string `json:"deviceId"`
	PublicKey string `json:"publicKey"`
	// Name is the client-reported display name (e.g. hostname). Optional and
	// only honored when the device has no name yet; admin renames win.
	Name string `json:"name,omitempty"`
}

type BindDeviceResp struct {
	DeviceID    string `json:"deviceId"`
	DeviceToken string `json:"deviceToken,omitempty"`
}

// AuthCodeInfo is the admin-facing view of an authorization code. Code holds
// the plaintext (a masked hint only for legacy records generated before
// plaintext persistence); the non-secret public ID is used for revocation. A
// single code may bind up to MaxBindings devices; BoundCount reports how many
// are bound.
type AuthCodeInfo struct {
	ID            string                `json:"id"`
	Code          string                `json:"code"` // plaintext code, e.g. "7K3H2M9Q"
	MaxBindings   int                   `json:"maxBindings"`
	BoundCount    int                   `json:"boundCount"`
	BoundToDevice string                `json:"boundToDevice,omitempty"` // first bound device (backward compatible)
	BoundAt       string                `json:"boundAt,omitempty"`
	BoundDevices  []AuthCodeBindingInfo `json:"boundDevices,omitempty"`
	CreatedAt     string                `json:"createdAt"`
	ExpiresAt     string                `json:"expiresAt,omitempty"` // ISO 8601, empty means permanent
	Status        string                `json:"status"`              // "active", "expired", "permanent"
}

// AuthCodeBindingInfo describes one device bound to a shared authorization
// code.
type AuthCodeBindingInfo struct {
	DeviceID string `json:"deviceId"`
	// DeviceName is the device's display name (empty when never reported).
	DeviceName string `json:"deviceName,omitempty"`
	BoundAt    string `json:"boundAt,omitempty"`
}

type AdminGenerateAuthCodesReq struct {
	Count       int        `json:"count"`
	MaxBindings int        `json:"maxBindings"` // devices a single code may bind; 0 defaults to 1
	ExpiresAt   *time.Time `json:"expiresAt"`   // expiration time, nil means permanent
}

type AdminGenerateAuthCodesResp struct {
	Codes []AuthCodeInfo `json:"codes"` // full code info including expiresAt
}

type AdminRenewAuthCodeReq struct {
	ExpiresAt *time.Time `json:"expiresAt"` // new expiration time, nil means permanent
}

type DeviceAuthStatusResp struct {
	Bound      bool       `json:"bound"`
	AuthCodeID string     `json:"authCodeId,omitempty"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty"`
	Expired    bool       `json:"expired"`
	Message    string     `json:"message,omitempty"`
}

// DeviceNetworkDetail contains the full information for a device's membership
// in a network, including the per-node credentials needed to reconstruct
// the client config after a reinstall.
type DeviceNetworkDetail struct {
	Network
	NodeID    string `json:"nodeId"`
	IP        string `json:"ip"`
	Token     string `json:"token"`
	Owner     bool   `json:"owner,omitempty"`
	PublicKey string `json:"publicKey,omitempty"`
}

// DeviceNetworksResp is the response for the device-scoped networks endpoint.
type DeviceNetworksResp struct {
	Networks []DeviceNetworkDetail `json:"networks"`
}

// UpdateNodePublicKeyReq updates a node's WireGuard public key.
type UpdateNodePublicKeyReq struct {
	PublicKey string `json:"publicKey"`
	DeviceID  string `json:"deviceId"`
}

// SetNodeRoleReq sets a node's role within a network. Only the owner may
// change roles; valid roles are "member" and "owner" (ownership transfer).
type SetNodeRoleReq struct {
	Role string `json:"role"`
}
