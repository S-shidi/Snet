package protocol

type WGPeerSummary struct {
	PeerID     string `json:"peerId"`
	PublicKey  string `json:"publicKey"`
	AllowedIPs string `json:"allowedIps"`
	Endpoint   string `json:"endpoint"`
	LastSeenAt int64  `json:"lastSeenAt"`
}
