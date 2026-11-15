/* ── Shared types for SNET client UI ────────────────────────────── */

/** Official coordination server used when the user leaves SnetServ empty. */
export const DEFAULT_SERVER = "https://snet.uizhi.eu.org:8090";

/** Compatibility alias for older UI code. */
export const FIXED_SERVER = DEFAULT_SERVER;

export type PeerStats = { RxBytes?: number; TxBytes?: number; LastHandshakeSec?: number };
export type PeerDirect = {
  path?: string;
  kind?: string;
  addr?: string;
  lan?: string;
  ipv6?: string;
  public?: string;
  lastRecvSec?: number;
};
export type RelayPath = {
  mode?: string;
  relay?: string;
  ready?: boolean;
  natType?: string;
  directReady?: number;
  directPeers?: number;
  relayReady?: number;
  relayCandidates?: number;
  peerDirect?: Record<string, PeerDirect>;
};
export type NetInfo = {
  networkId: string;
  name?: string;
  ip?: string;
  subnet?: string;
  port?: number;
  active?: boolean;
  owner?: boolean;
  error?: string;
  state?: string;
  interface?: string;
  peerStats?: Record<string, PeerStats>;
  joinedAt?: string;
  description?: string;
  tags?: string[];
  visibility?: string;
  role?: string;
  serverState?: string;
  relayPath?: RelayPath;
  hubNodeId?: string;
  useHubNode?: boolean;
};
export type PendingJoin = {
  pendingId: string;
  networkId: string;
  createdAt?: string;
  status?: string;
  error?: string;
};
export type NatMappingInfo = {
  port: number;
  externalIp: string;
  externalPort: number;
  via: string;
  lease?: number;
};
export type NatErrorInfo = { port: number; error: string; lastTry?: string };
export type NatStatus = { enabled: boolean; mappings: NatMappingInfo[]; errors: NatErrorInfo[] };

/** A delegated relay route: the owner assigns a CIDR to a node that acts as
 *  the gateway for it. "pending" until the target node confirms. */
export type RelayRoute = {
  targetNodeId: string;
  cidr: string;
  status?: "pending" | "active";
};

export type DaemonStatus = {
  deviceId?: string;
  serverAddr?: string;
  wgPort?: number;
  rxSpeed?: number;
  txSpeed?: number;
  version?: string;
  networks?: NetInfo[];
  pendingJoins?: PendingJoin[];
  natMapping?: NatStatus;
  /** Whether this device may act as a relay gateway for the network. */
  allowRelay?: boolean;
  /** Whether the current platform supports the delegated relay gateway. */
  relayGatewaySupported?: boolean;
  /** Whether this device honours the network's delegated relay routes. */
  useRelay?: boolean;
  /** Why the local relay gateway setup failed (empty when healthy). */
  relayError?: string;
};
export type NetInfoDetail = {
  id?: string;
  networkId?: string;
  name?: string;
  subnet?: string;
  approvalRequired?: boolean;
  pendingCount?: number;
  nodeCount?: number;
  pairingCode?: string;
  description?: string;
  tags?: string[];
  visibility?: string;
  routes?: RelayRoute[];
  pending?: Array<{ id: string; publicKey: string; deviceId?: string; createdAt?: string; deviceName?: string }>;
  nodes?: Array<{ id: string; ip: string; deviceId?: string; deviceName?: string; online?: boolean; role?: string; publicKey?: string; allowRelay?: boolean; relayError?: string }>;
};
export type NetNode = { id: string; ip: string; publicKey?: string; deviceId?: string; deviceName?: string; online?: boolean; role?: string; natType?: string; allowRelay?: boolean; relayError?: string; endpoint?: string; endpointV6?: string; publicIPv4?: string; publicIPv6?: string; isHub?: boolean };
export type GatewayInfo = { id: string; name: string; canBeGateway: boolean; reason?: string; joined: boolean; online?: boolean; isHub?: boolean };
export type WireGuardExport = { nodeId: string; networkId: string; gatewayNodeId?: string; gatewayName?: string; config: string; createdAt: string };
export type WireGuardConfigResp = { nodeId: string; config: string };
export type PeersResp = { peers?: NetNode[]; self?: NetNode; subnet?: string; approvalRequired?: boolean; routes?: RelayRoute[] };
export type CreateResp = { networkId: string; ip: string; pairingCode: string; link: string };  
export type JoinResp = { ip?: string; networkId?: string; status?: string; pendingId?: string };
/** The current invite link of a network. The pairing code behind it is never
 *  exposed: the link is the only thing a member needs to join. */
export type InviteLinkResp = { networkId: string; name?: string; link: string };
/** The result of rotating the invite link: the previous link is dead, the
 *  returned one replaces it. */
export type RotateInviteResp = { networkId: string; name?: string; pairingCode?: string; link: string };
export type ShareMeta = { description?: string; tags?: string[]; visibility?: string };
/** Desktop daemon supervisor state (Windows Wails only). */
export type DesktopDaemonStatus = { running: boolean; starting: boolean; error?: string };

/* ── Backend interface: platform adapters implement this ──────── */
export interface Backend {
  /** Get daemon status (returns null on failure) */
  status(): Promise<DaemonStatus | null>;
  /** Create a network */
  create(params: { server: string; port: number; name: string; subnet: string; approvalRequired: boolean; description?: string; tags?: string[]; visibility?: string }): Promise<CreateResp>;
  /** Join a network */
  join(params: { server: string; port: number; link: string }): Promise<JoinResp>;
  /** Validate the target SnetServ before saving */
  checkServer(serverAddr: string): Promise<void>;
  /** Save the SnetServ address and reconnect */
  setServer(serverAddr: string): Promise<void>;
  /** Rejoin (connect) a network */
  rejoin(nid: string): Promise<void>;
  /** Leave (disconnect) a network */
  leave(nid: string): Promise<void>;
  /** Remove network config from device */
  remove(nid: string): Promise<void>;
  /** Delete network from server (owner only) */
  deleteNet(nid: string): Promise<void>;
  /** Get network info (owner detail) */
  netinfo(nid: string): Promise<NetInfoDetail>;
  /** Get peers for a network */
  peers(nid: string): Promise<PeersResp>;
  /** List WireGuard gateway candidates (owner only) */
  gateways(nid: string): Promise<GatewayInfo[]>;
  /** Export a standard WireGuard config through a gateway (owner only) */
  wireguardConfig(nid: string, gatewayNodeID: string): Promise<WireGuardConfigResp>;
  /** Locally saved WireGuard exports for a network */
  wireguardExports(nid: string): Promise<WireGuardExport[]>;
  /** Update network settings (owner only) */
  updateSettings(params: { nid: string; name: string; subnet: string; approvalRequired: boolean | null }): Promise<void>;
  /** Enable or disable automatic inbound NAT mapping for this device */
  setNATMapping(enabled: boolean): Promise<void>;
  /** Set a peer node's role (owner only) */
  setRole?(params: { nid: string; nodeId: string; role: string }): Promise<void>;
  /** Set the device's relay switches: act as a gateway, and use the network's gateways */
  setRelayOptions(params: { allowRelay: boolean; useRelay: boolean }): Promise<void>;
  /** Rename this device on every joined network. */
  renameDevice(name: string): Promise<void>;
  /** Rename another device in a network (owner only). */
  renameNodeDevice(params: { nid: string; nodeId: string; name: string }): Promise<void>;
  /** Change another device's virtual IP (owner only). */
  setNodeIP(params: { nid: string; nodeId: string; ip: string }): Promise<void>;
  /** Replace the network's delegated relay routes (owner only) */
  setRoutes(params: { nid: string; routes: RelayRoute[] }): Promise<void>;
  /** Kick a node from a network */
  kick(params: { nid: string; nodeId: string }): Promise<void>;
  /** Approve a pending join request */
  approve(params: { nid: string; pendingId: string }): Promise<void>;
  /** Deny a pending join request */
  deny(params: { nid: string; pendingId: string }): Promise<void>;
  /** Cancel a pending join request */
  cancelPending(pendingId: string): Promise<void>;
  /** Get the current invite link of a network (owner only) */
  inviteLink(nid: string): Promise<InviteLinkResp>;
  /** Rotate the invite link of a network (owner only); old links stop working */
  rotateInvite(nid: string): Promise<RotateInviteResp>;
  /** Detect local subnets */
  detectLocalSubnets(): Promise<string[]>;
  /** Ping a peer IP and return loss percentage and average RTT */
  ping(target: string): Promise<{ loss: number; avgRtt: number }>;
  /** Send a short UDP probe through the tunnel to wake a peer */
  poke(target: string): Promise<void>;
  /** Fetch recent daemon diagnostic logs */
  logs(): Promise<string>;
  /** Ensure daemon is running (Desktop only, Web returns error) */
  ensureDaemon(): Promise<void>;
  /** Desktop supervisor state (Windows Wails only) */
  daemonStatus?(): Promise<DesktopDaemonStatus>;
  /** Scan a QR code and return the decoded text (Android native only) */
  scanQR?(): Promise<string>;
  /** Whether the backend supports daemon lifecycle management */
  hasDaemonControl: boolean;
}
