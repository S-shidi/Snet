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
	addr := flag.String("addr", "0.0.0.0:8090", "public coordination listen address")
	probeAddr := flag.String("probe-addr", "[::]:8091", "UDP probe/echo address for public-IP discovery (dual-stack)")
	dbPath := flag.String("db", "", "bbolt database path (empty = in-memory, not persistent)")
	publicURL := flag.String("public-url", os.Getenv("SNETSERV_PUBLIC_URL"), "canonical public coordination URL embedded in invites")
	serverID := flag.String("server-id", os.Getenv("SNETSERV_ID"), "stable federation server identity (auto-generated and persisted when empty)")
	internalAddr := flag.String("internal-addr", "127.0.0.1:8096", "loopback-only internal admin RPC listen address")
	internalToken := flag.String("internal-token", os.Getenv("SNET_INTERNAL_ADMIN_TOKEN"), "internal admin service token (env SNET_INTERNAL_ADMIN_TOKEN)")
	internalAllowCIDR := flag.String("internal-allow-cidr", os.Getenv("SNET_INTERNAL_ADMIN_ALLOW_CIDR"), "comma-separated private CIDRs allowed to reach the internal Admin RPC (loopback is always allowed)")
	zombieTTL := flag.Duration("zombie-ttl", 72*time.Hour, "delete networks idle for this long; 0 disables reaping")
	trustProxy := flag.Bool("behind-proxy", false, "read client IP from X-Forwarded-For (reverse proxy deployments)")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM); enables HTTPS when set with -tls-key")
	tlsKey := flag.String("tls-key", "", "TLS key file (PEM)")
	relayHost := flag.String("relay-host", "", "public relay host (IP) advertised to peers; enables UDP relay mode")
	relayBase := flag.Int("relay-base", protocol.DefaultWGPort, "base UDP relay port (one per network)")
	relayCount := flag.Int("relay-count", 256, "number of assignable UDP relay ports (bound lazily per active network)")
	relayAlternates := flag.String("relay-alternates", "", "comma-separated additional relay hosts advertised for multi-relay selection")
	relayV2Endpoint := flag.String("relay-v2-endpoint", os.Getenv("SNET_RELAY_V2_ENDPOINT"), "standalone Relay v2 UDP endpoint advertised to clients")
	relayV2Endpoints := flag.String("relay-v2-endpoints", os.Getenv("SNET_RELAY_V2_ENDPOINTS"), "comma-separated Relay v2 endpoints for failover")
	relaySecret := flag.String("relay-secret", os.Getenv("SNET_RELAY_SECRET"), "Relay v2 ticket HMAC secret")
	relaySecretOld := flag.String("relay-secret-old", os.Getenv("SNET_RELAY_SECRET_OLD"), "previous Relay v2 secret for rotation")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}
	log.Printf("SnetServ %s", buildinfo.String())

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
		store.SetRelayEnsure(relay.Ensure)
		store.SetNodeEnsure(relay.EnsureNode)
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

	publicSrv := &http.Server{
		Addr:              *addr,
		Handler:           server.NewCoordinationHandler(store, server.Options{ZombieTTL: *zombieTTL, TrustProxy: *trustProxy}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	internalSrv := &http.Server{
		Addr:              *internalAddr,
		Handler:           server.NewInternalAdminHandler(store, server.Options{ZombieTTL: *zombieTTL, TrustProxy: *trustProxy, InternalAdminAllowCIDRs: splitCSV(*internalAllowCIDR)}, *internalToken),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Printf("SnetServ public API listening on %s (db: %s)", *addr, dbPathOrMem(*dbPath))
		var err error
		if *tlsCert != "" && *tlsKey != "" {
			err = publicSrv.ListenAndServeTLS(*tlsCert, *tlsKey)
		} else {
			err = publicSrv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()
	go func() {
		if strings.TrimSpace(*internalToken) == "" {
			log.Printf("internal admin RPC disabled: -internal-token/SNET_INTERNAL_ADMIN_TOKEN is empty")
			return
		}
		log.Printf("internal admin RPC listening on %s (loopback only)", *internalAddr)
		if err := internalSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.Printf("SnetServ: %v", err)
	case <-stop:
	}

	log.Printf("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = publicSrv.Shutdown(ctx)
	_ = internalSrv.Shutdown(ctx)
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
