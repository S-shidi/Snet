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
	"syscall"
	"time"

	"snet/internal/buildinfo"
	"snet/internal/relay"
)

func main() {
	addr := flag.String("addr", ":8092", "Relay v2 UDP listen address")
	metricsAddr := flag.String("metrics-addr", ":8094", "metrics/health HTTP address (empty disables)")
	secret := flag.String("secret", os.Getenv("SNET_RELAY_SECRET"), "ticket HMAC secret (env SNET_RELAY_SECRET)")
	secretOld := flag.String("secret-old", os.Getenv("SNET_RELAY_SECRET_OLD"), "previous ticket HMAC secret for rotation (env SNET_RELAY_SECRET_OLD)")
	sessionTTL := flag.Duration("session-ttl", 30*time.Second, "idle session TTL")
	maxSessions := flag.Int("max-sessions", 4096, "maximum concurrent relay sessions")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}
	if *secret == "" {
		log.Fatal("relay: -secret or SNET_RELAY_SECRET is required")
	}
	signer, err := relay.NewTicketSigner(*secret, *secretOld)
	if err != nil {
		log.Fatalf("relay: %v", err)
	}
	srv, err := relay.Listen(*addr, signer, relay.Options{SessionTTL: *sessionTTL, MaxSessions: *maxSessions})
	if err != nil {
		log.Fatalf("relay: %v", err)
	}
	defer srv.Close()
	log.Printf("snet-relay %s listening on %s (metrics %s)", buildinfo.String(), srv.Addr(), *metricsAddr)

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(); err != nil {
			serveErr <- err
		}
	}()

	var metricsSrv *http.Server
	if *metricsAddr != "" {
		metricsSrv = &http.Server{Addr: *metricsAddr, Handler: metricsHandler(srv), ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				serveErr <- err
			}
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serveErr:
		log.Printf("relay: %v", err)
	case <-stop:
	}
	if metricsSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = metricsSrv.Shutdown(ctx)
		cancel()
	}
}

func metricsHandler(srv *relay.Server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		st := srv.Stats()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","sessions":%d,"packetsIn":%d,"packetsOut":%d}`+"\n",
			st.Sessions, st.PacketsIn, st.PacketsOut)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		st := srv.Stats()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE snet_relay_sessions gauge\nsnet_relay_sessions %d\n", st.Sessions)
		fmt.Fprintf(w, "# TYPE snet_relay_packets_in_total counter\nsnet_relay_packets_in_total %d\n", st.PacketsIn)
		fmt.Fprintf(w, "# TYPE snet_relay_packets_out_total counter\nsnet_relay_packets_out_total %d\n", st.PacketsOut)
		fmt.Fprintf(w, "# TYPE snet_relay_bytes_in_total counter\nsnet_relay_bytes_in_total %d\n", st.BytesIn)
		fmt.Fprintf(w, "# TYPE snet_relay_bytes_out_total counter\nsnet_relay_bytes_out_total %d\n", st.BytesOut)
		fmt.Fprintf(w, "# TYPE snet_relay_drops_total counter\nsnet_relay_drops_total %d\n", st.Drops)
		fmt.Fprintf(w, "# TYPE snet_relay_errors_total counter\nsnet_relay_errors_total %d\n", st.Errors)
		fmt.Fprintf(w, "# TYPE snet_relay_control_drops_total counter\nsnet_relay_control_drops_total %d\n", st.ControlDrops)
	})
	return mux
}
