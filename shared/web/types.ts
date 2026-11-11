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
  allowedSubnets?: string[];
  joinedAt?: string;
  description?: string;
  tags?: string[];
  visibility?: string;
  role?: string;
  serverState?: string;
  relayPath?: RelayPath;
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
  pending?: Array<{ id: string; publicKey: string; deviceId?: string; createdAt?: string; deviceName?: string }>;
  nodes?: Array<{ id: string; ip: string; deviceId?: string; online?: boolean; allowedSubnets?: string[]; role?: string; publicKey?: string }>;
};
export type NetNode = { id: string; ip: string; publicKey?: string; deviceId?: string; deviceName?: string; online?: boolean; allowedSubnets?: string[]; role?: string; natType?: string };
export type PeersResp = { peers?: NetNode[]; self?: NetNode; subnet?: string; approvalRequired?: boolean };
export type CreateResp = { networkId: string; ip: string; pairingCode: string; link: string };  
export type JoinResp = { ip?: string; networkId?: string; status?: string; pendingId?: string };
export type ShareMeta = { description?: string; tags?: string[]; visibility?: string };

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
  /** Update network settings (owner only) */
  updateSettings(params: { nid: string; name: string; subnet: string; approvalRequired: boolean | null }): Promise<void>;
  /** Enable or disable automatic inbound NAT mapping for this device */
  setNATMapping(enabled: boolean): Promise<void>;
  /** Set a peer node's role (owner only) */
  setRole?(params: { nid: string; nodeId: string; role: string }): Promise<void>;
  /** Update subnets for a network */
  updateSubnets(params: { nid: string; subnets: string[] }): Promise<void>;
  /** Kick a node from a network */
  kick(params: { nid: string; nodeId: string }): Promise<void>;
  /** Approve a pending join request */
  approve(params: { nid: string; pendingId: string }): Promise<void>;
  /** Deny a pending join request */
  deny(params: { nid: string; pendingId: string }): Promise<void>;
  /** Cancel a pending join request */
  cancelPending(pendingId: string): Promise<void>;
  /** Reset pairing code */
  resetCode(nid: string): Promise<{ pairingCode: string }>;
  /** Detect local subnets */
  detectLocalSubnets(): Promise<string[]>;
  /** Ping a peer IP and return loss percentage and average RTT */
  ping(target: string): Promise<{ loss: number; avgRtt: number }>;
  /** Fetch recent daemon diagnostic logs */
  logs(): Promise<string>;
  /** Ensure daemon is running (Desktop only, Web returns error) */
  ensureDaemon(): Promise<void>;
  /** Scan a QR code and return the decoded text (Android native only) */
  scanQR?(): Promise<string>;
  /** Whether the backend supports daemon lifecycle management */
  hasDaemonControl: boolean;
}
