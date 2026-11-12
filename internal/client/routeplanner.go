package client

import (
	"snet/internal/protocol"
)

// RoutePlanner turns the network's delegated relay routes into effective
// peer routing entries.
//
// A delegated route says "node X is the gateway for CIDR C". Every other
// member must therefore send traffic for C through node X's tunnel, exactly
// like a self-advertised subnet: node X's WireGuard AllowedIPs must cover C
// and the host needs an OS route for C via the tunnel. The planner merges the
// delegated CIDRs into the target peer's routing entries so the existing
// AllowedIPs + OS route installation handles them with no further changes.
//
// When useRelay is false the planner ignores every delegated route: the
// network's relay gateways are then not used at all.
type RoutePlanner struct {
	// selfID is the caller's own node ID. Routes delegated to it are skipped:
	// the gateway must not send its own delegated traffic back into the
	// tunnel, it has to forward it towards the LAN.
	selfID string
	// useRelay mirrors the local "use relay gateways" switch.
	useRelay bool
}

// NewRoutePlanner returns a planner for the given node and switch state.
func NewRoutePlanner(selfID string, useRelay bool) *RoutePlanner {
	return &RoutePlanner{selfID: selfID, useRelay: useRelay}
}

// Apply returns the peer list with the delegated routes injected. The input
// slice is never modified; a new slice is returned when something changed.
//
// Only routes in the "active" state are applied. A pending route has no
// working gateway yet (the target node has not confirmed it can forward), so
// installing it would blackhole the delegated subnet.
func (p *RoutePlanner) Apply(peers []protocol.Node, routes []protocol.Route) []protocol.Node {
	if p == nil || !p.useRelay || len(routes) == 0 || len(peers) == 0 {
		return peers
	}
	byTarget := make(map[string][]string)
	for _, r := range routes {
		if r.Status != protocol.RouteStatusActive {
			continue
		}
		if r.CIDR == "" || r.TargetNodeID == "" {
			continue
		}
		if r.TargetNodeID == p.selfID {
			continue
		}
		byTarget[r.TargetNodeID] = append(byTarget[r.TargetNodeID], r.CIDR)
	}
	if len(byTarget) == 0 {
		return peers
	}
	out := make([]protocol.Node, len(peers))
	copy(out, peers)
	changed := false
	for i := range out {
		cidrs, ok := byTarget[out[i].ID]
		if !ok {
			continue
		}
		merged := mergeCIDRs(out[i].AllowedSubnets, cidrs)
		if len(merged) != len(out[i].AllowedSubnets) {
			changed = true
		}
		out[i].AllowedSubnets = merged
	}
	if !changed {
		return peers
	}
	return out
}

// ServedBySelf returns the CIDRs that are delegated to this node itself. The
// node forwards them towards the LAN instead of back into the tunnel, so
// they are not part of its own routing entries — but knowing them makes the
// gateway role visible in the logs.
func (p *RoutePlanner) ServedBySelf(routes []protocol.Route) []string {
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		if r.Status != protocol.RouteStatusActive {
			continue
		}
		if r.CIDR == "" || r.TargetNodeID == "" || r.TargetNodeID != p.selfID {
			continue
		}
		out = append(out, r.CIDR)
	}
	return out
}

// mergeCIDRs appends the extra CIDRs to the base list, skipping duplicates
// and preserving the original order.
func mergeCIDRs(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, c := range base {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	for _, c := range extra {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}
