package protocol

type HubMode string

const (
	HubModeEmbedded   HubMode = "embedded"
	HubModeStandalone HubMode = "standalone"
	HubModeHybrid     HubMode = "hybrid"
)
