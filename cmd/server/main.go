// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"snet/internal/buildinfo"
	"snet/internal/protocol"
	snetrelay "snet/internal/relay"
	"snet/internal/server"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:8090", "listen address")
	probeAddr := flag.String("probe-addr", "[::]:8091", "UDP probe/echo address for public-IP discovery (dual-stack)")
	dbPath := flag.String("db", "", "bbolt database path (empty = in-memory, not persistent)")
	publicURL := flag.String("public-url", os.Getenv("SNETSERV_PUBLIC_URL"), "canonical public coordination URL embedded in invites")
	serverID := flag.String("server-id", os.Getenv("SNETSERV_ID"), "stable federation server identity (auto-generated and persisted when empty)")
	adminToken := flag.String("admin-token", os.Getenv("SNET_ADMIN_TOKEN"), "admin API token (env SNET_ADMIN_TOKEN)")
	adminUser := flag.String("admin-user", os.Getenv("SNET_ADMIN_USER"), "admin login username (env SNET_ADMIN_USER)")
	adminPass := flag.String("admin-pass", os.Getenv("SNET_ADMIN_PASSWORD"), "admin login password (env SNET_ADMIN_PASSWORD)")
	zombieTTL := flag.Duration("zombie-ttl", 72*time.Hour, "delete networks idle for this long; 0 disables reaping")
	trustProxy := flag.Bool("behind-proxy", false, "read client IP from X-Forwarded-For (reverse proxy deployments)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM); enables HTTPS when set with -tls-key")
	tlsKey := flag.String("tls-key", "", "TLS key file (PEM)")
	relayHost := flag.String("relay-host", "", "public relay host (IP) advertised to peers; enables UDP relay mode")
	relayBase := flag.Int("relay-base", protocol.DefaultWGPort, "base UDP relay port (one per network)")
	relayCount := flag.Int("relay-count", 256, "number of assignable UDP relay ports (bound lazily per active network)")
	relayAlternates := flag.String("relay-alternates", "", "comma-separated additional relay hosts advertised for multi-relay selection (each served by its own relay instance)")
	relayV2Endpoint := flag.String("relay-v2-endpoint", os.Getenv("SNET_RELAY_V2_ENDPOINT"), "standalone Relay v2 UDP endpoint advertised to clients (env SNET_RELAY_V2_ENDPOINT)")
	relayV2Endpoints := flag.String("relay-v2-endpoints", os.Getenv("SNET_RELAY_V2_ENDPOINTS"), "comma-separated Relay v2 endpoints for failover; overrides -relay-v2-endpoint (env SNET_RELAY_V2_ENDPOINTS)")
	relaySecret := flag.String("relay-secret", os.Getenv("SNET_RELAY_SECRET"), "Relay v2 ticket HMAC secret (env SNET_RELAY_SECRET)")
	relaySecretOld := flag.String("relay-secret-old", os.Getenv("SNET_RELAY_SECRET_OLD"), "previous Relay v2 secret for rotation (env SNET_RELAY_SECRET_OLD)")
	adminReset := flag.Bool("admin-reset", false, "force-reset admin password from env/admin-pass and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}
	log.Printf("Snet server %s", buildinfo.String())

	store, err := server.NewStoreAt(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	resolvedID, err := store.EnsureServerID(*serverID)
	if err != nil {
		log.Fatalf("server id: %v", err)
	}
	if *dbPath == "" {
		log.Printf("WARNING: in-memory database; server ID %s is temporary. Federation requires a persistent database.", resolvedID)
	} else {
		log.Printf("server id: %s", resolvedID)
	}

	store.SetSnetServURL(*publicURL)
	if backfilled, err := store.BackfillNetworkIdentity(); err != nil {
		log.Fatalf("backfill network identity: %v", err)
	} else if backfilled > 0 {
		log.Printf("backfilled snetServId/snetServUrl on %d networks", backfilled)
	}

	if *adminReset {
		if *adminUser == "" || *adminPass == "" {
			log.Fatal("-admin-reset requires -admin-user and -admin-pass (or SNET_ADMIN_USER/SNET_ADMIN_PASSWORD env)")
		}
		if _, err := store.SetAdminPassword(*adminUser, *adminPass); err != nil {
			log.Fatalf("reset admin password: %v", err)
		}
		log.Printf("admin password reset for user %q", *adminUser)
		return
	}

	if err := store.SetHubNode(os.Getenv("SNET_HUB_NODE_ID"), os.Getenv("SNET_HUB_PUBLIC_KEY"), os.Getenv("SNET_HUB_ENDPOINT")); err != nil {
		log.Fatalf("official hub config: %v", err)
	}
	store.SetRelay(*relayHost, *relayBase, *relayCount)
	if *relayAlternates != "" {
		store.SetRelayAlternates(strings.Split(*relayAlternates, ","))
	}

	v2Endpoints := splitCSV(*relayV2Endpoints)
	if len(v2Endpoints) == 0 {
		v2Endpoints = splitCSV(*relayV2Endpoint)
	}
	if len(v2Endpoints) > 0 {
		if *relaySecret == "" {
			log.Printf("relay v2 disabled: an endpoint is set but -relay-secret/SNET_RELAY_SECRET is empty")
		} else {
			signer, err := snetrelay.NewTicketSigner(*relaySecret, *relaySecretOld)
			if err != nil {
				log.Fatalf("relay v2 secret: %v", err)
			}
			store.SetRelayV2Endpoints(v2Endpoints, signer)
			log.Printf("relay v2: tickets for %s", strings.Join(v2Endpoints, ", "))
		}
	}

	var relay *server.Relay
	if *relayHost != "" {
		relay = server.NewRelay(*relayBase, *relayCount)
		relay.SetActivityHook(store.MarkRelayActivity)
		relay.SetFlowHook(store.OnRelayFlow)
		// Lazy binding: a network's relay port is bound on first use instead
		// of binding the whole pool at startup. Decouples the relay's port
		// footprint from the assignable pool size.
		store.SetRelayEnsure(relay.Ensure)
		// Phase 3: per-node unicast relay ports. Each node's port is bound
		// lazily the same way and routes unicast instead of broadcasting.
		store.SetNodeEnsure(relay.EnsureNode)
		// Release hook: when a network or node is removed, the store frees the
		// bound relay socket and its send-loop goroutine so ports are never
		// leaked out of the assignable pool.
		store.SetRelayRelease(relay.ClosePort)
		relay.SetNodeRoute(store.RelayRouteNodePort)
		store.SetRelayAllFlows(relay.Flows)
		store.SetRelaySend(relay.SendFrom)
		store.SetRelaySendFanned(relay.SendFanned)
		defer relay.Close()
		log.Printf("relay: %d assignable UDP ports from %d via %s (lazy binding)", *relayCount, *relayBase, *relayHost)
	}

	if *zombieTTL > 0 {
		go func() {
			ticker := time.NewTicker(10 * time.Minute)
			defer ticker.Stop()
			for range ticker.C {
				if victims, err := store.SweepZombies(*zombieTTL); err != nil {
					log.Printf("zombie sweep: %v", err)
				} else if len(victims) > 0 {
					log.Printf("zombie sweep: removed %d idle networks: %v", len(victims), victims)
				}
			}
		}()
		log.Printf("zombie sweep: networks idle > %s will be deleted", *zombieTTL)
	}

	probe, err := server.StartProbeServer(*probeAddr)
	if err != nil {
		log.Fatalf("probe server: %v", err)
	}
	defer probe.Close()

	adminEnabled := *adminToken != "" || (*adminUser != "" && *adminPass != "")
	if !adminEnabled {
		log.Printf("admin: no credentials configured yet; initialize via the web setup page (http://<host>:%s/admin)", strings.Split(*addr, ":")[len(strings.Split(*addr, ":"))-1])
	}
	httpSrv := &http.Server{
		Addr: *addr,
		Handler: server.NewHandler(store, server.Options{
			AdminToken: *adminToken,
			AdminUser:  *adminUser,
			AdminPass:  *adminPass,
			ZombieTTL:  *zombieTTL,
			TrustProxy: *trustProxy,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MiB: cap header size to bound memory
	}

	if *tlsCert == "" || *tlsKey == "" {
		log.Printf("warning: TLS is not configured; admin credentials would cross the network in cleartext. Consider -tls-cert/-tls-key.")
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Snet server listening on %s (db: %s, admin: %s)", *addr, dbPathOrMem(*dbPath), adminState(adminEnabled))
		var err error
		if *tlsCert != "" && *tlsKey != "" {
			err = httpSrv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			serveErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		// Return normally so the deferred store/relay/probe closes still run.
		log.Fatalf("server: %v", err)
	case <-stop:
	}

	log.Printf("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func splitCSV(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func dbPathOrMem(p string) string {
	if p == "" {
		return "memory"
	}
	return p
}

func adminState(enabled bool) string {
	if !enabled {
		return "disabled"
	}
	return "enabled"
}
