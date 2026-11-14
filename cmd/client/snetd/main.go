package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"snet/internal/buildinfo"
	"snet/internal/client"
)

func main() {
	if handled, code := handleServiceCommand(os.Args[1:]); handled {
		os.Exit(code)
	}
	// "service" is an explicit alias for the SCM-started mode. The SCM itself
	// invokes snetd with no arguments.
	if len(os.Args) > 1 && strings.EqualFold(os.Args[1], "service") {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

	ctlAddr := flag.String("ctl", "127.0.0.1:19432", "local control API address")
	configPath := flag.String("config", "", "config file path (default: user config dir)")
	deviceIDFile := flag.String("device-id-file", client.DefaultDeviceIDFile, "path to the stable device identity file (empty to disable)")
	logFile := flag.String("log-file", "", "append daemon logs to this file (empty: stderr + in-memory ring)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(buildinfo.String())
		return
	}
	writers := []io.Writer{os.Stderr, client.LogWriter()}
	if *logFile != "" {
		if f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			defer f.Close()
			writers = append(writers, f)
		} else {
			log.Printf("open log file %s: %v", *logFile, err)
		}
	}
	log.SetOutput(io.MultiWriter(writers...))
	log.Printf("snetd %s", buildinfo.String())

	cfg, err := client.LoadConfigAt(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	runDaemon(cfg, *configPath, *ctlAddr, *deviceIDFile)
}
