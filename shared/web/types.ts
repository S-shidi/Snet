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
  publicIP?: string;
  localSubnets?: string[];
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
  /** CIDRs advertised by peers and routed through this network. */
  peerSubnets?: string[];
  joinedAt?: string;
  description?: string;
  tags?: string[];
  visibility?: string;
  role?: string;
  serverState?: string;
  /** Coordination server bound to this network. */
  snetServUrl?: string;
  /** Stable identity of the coordination server. */
  snetServId?: string;
  relayPath?: RelayPath;
  hubNodeId?: string;
  useHubNode?: boolean;
};
export type InviteInfo = { networkId: string; name?: string; server: string };
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
  /** Client-local preference; missing means enabled. */
  enabled?: boolean;
  /** Why an active route is not installed on this client. */
  localState?: "self" | "local-conflict";
  /** User-facing explanation for localState. */
  localReason?: string;
};

export type DaemonStatus = {
  deviceId?: string;
  deviceName?: string;
  wgPort?: number;
  rxSpeed?: number;
  txSpeed?: number;
  version?: string;
  platform?: string;
  upgradeSupported?: boolean;
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
  /** One-shot message when a joined network was removed server-side. */
  notice?: { seq?: number; text?: string };
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
  snetServUrl?: string;
  snetServId?: string;
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

export type UpdateCheckResp = {
  currentVersion?: string;
  latestVersion: string;
  hasUpdate: boolean;
  upgradeSupported?: boolean;
  platform?: string;
  releaseUrl: string;
  publishedAt?: string;
  releaseNotes?: string;
  assetName?: string;
  assetUrl?: string;
  sha256Url?: string;
  assets?: Record<string, string>;
};

export type UpgradeProgress = {
  state: "idle" | "downloading" | "verifying" | "installing" | "restarting" | "ready" | "error";
  version?: string;
  message?: string;
  downloaded: number;
  total: number;
  percent: number;
  error?: string;
};

/* ── Backend interface: platform adapters implement this ──────── */
export interface Backend {
  /** Get daemon status (returns null on failure) */
  status(): Promise<DaemonStatus | null>;
  /** Create a network */
  create(params: { server: string; port: number; name: string; subnet: string; approvalRequired: boolean; description?: string; tags?: string[]; visibility?: string }): Promise<CreateResp>;
  /** Inspect an invite without joining: decrypts the bound SnetServ. */
  inspectInvite(link: string): Promise<InviteInfo>;
  /** Join a network */
  join(params: { port: number; link: string }): Promise<JoinResp>;
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
  /** Set whether this device acts as a relay gateway. */
  setRelayOptions(params: { allowRelay: boolean; useRelay?: boolean }): Promise<void>;
  /** Enable or disable one delegated CIDR on this device. */
  setRouteEnabled(params: { nid: string; cidr: string; enabled: boolean }): Promise<void>;
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
  /** Ping a peer IP and return loss percentage and average RTT */
  ping(target: string): Promise<{ loss: number; avgRtt: number }>;
  /** Send a short UDP probe through the tunnel to wake a peer */
  poke(target: string): Promise<void>;
  /** Fetch recent daemon diagnostic logs */
  logs(): Promise<string>;
  /** Save diagnostic logs to the local filesystem and return the path */
  saveLog?(text: string): Promise<string>;
  /** Ensure daemon is running (Desktop only, Web returns error) */
  ensureDaemon(): Promise<void>;
  /** Desktop supervisor state (Windows Wails only) */
  daemonStatus?(): Promise<DesktopDaemonStatus>;
  /** Check for client updates via the local daemon */
  checkUpdate?(platform?: string): Promise<UpdateCheckResp>;
  /** Start desktop one-click self-upgrade (macOS / Windows Wails) */
  startDesktopUpgrade?(params: { version: string; assetUrl?: string }): Promise<UpgradeProgress>;
  /** Poll desktop self-upgrade progress (macOS / Windows Wails) */
  desktopUpgradeStatus?(): Promise<UpgradeProgress>;
  /** Scan a QR code and return the decoded text (Android native only) */
  scanQR?(): Promise<string>;
  /** Whether the backend supports daemon lifecycle management */
  hasDaemonControl: boolean;
}
