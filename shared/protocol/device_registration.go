package protocol

type DeviceRegistration struct {
	DeviceID          string   `json:"deviceId"`
	NetworkSlug       string   `json:"networkSlug"`
	Capabilities      []string `json:"capabilities"`
	WGEndpoint        string   `json:"wgEndpoint,omitempty"`
	ClientProvidedKey bool     `json:"-"`
	Timestamp         int64    `json:"timestamp"`
}
