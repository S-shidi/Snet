//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"snet/internal/client"
)

const windowsServiceName = "SNetDaemon"

// handleServiceCommand implements the installer-facing commands before the
// normal daemon flags are parsed. SCM launches the daemon with no args, which
// keeps the normal runDaemon path.
func handleServiceCommand(args []string) (handled bool, exitCode int) {
	if len(args) == 0 {
		return false, 0
	}
	switch strings.ToLower(args[0]) {
	case "install":
		if err := installWindowsService(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "install service:", err)
			return true, 1
		}
		fmt.Printf("Windows service %s installed\n", windowsServiceName)
		return true, 0
	case "uninstall":
		if err := uninstallWindowsService(); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall service:", err)
			return true, 1
		}
		fmt.Printf("Windows service %s removed\n", windowsServiceName)
		return true, 0
	}
	return false, 0
}

func defaultServiceConfigPath() string {
	dir := strings.TrimSpace(os.Getenv("ProgramData"))
	if dir == "" {
		dir = `C:\ProgramData`
	}
	return filepath.Join(dir, "SNET", "config.json")
}

func installWindowsService(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	configPath := fs.String("config", defaultServiceConfigPath(), "daemon config file")
	deviceIDFile := fs.String("device-id-file", client.DefaultDeviceIDFile, "stable device id file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*configPath), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	serviceArgs := []string{"-config", *configPath, "-device-id-file", *deviceIDFile}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()

	cfg := mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: "SNET Virtual Network Daemon",
		Description: "Provides the SNET virtual network tunnel and local control API.",
	}
	var s *mgr.Service
	s, err = m.OpenService(windowsServiceName)
	if err == nil {
		stopWindowsService(s)
		cfg.BinaryPathName = windows.ComposeCommandLine(append([]string{exe}, serviceArgs...))
		if err := s.UpdateConfig(cfg); err != nil {
			s.Close()
			return fmt.Errorf("update service config: %w", err)
		}
	} else if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		s, err = m.CreateService(windowsServiceName, exe, cfg, serviceArgs...)
		if err != nil {
			return fmt.Errorf("create service: %w", err)
		}
	} else {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()

	if err := allowAuthenticatedUsersToStart(s); err != nil {
		return fmt.Errorf("grant service start permission: %w", err)
	}
	if st, queryErr := s.Query(); queryErr == nil && st.State == svc.Stopped {
		if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return fmt.Errorf("start service: %w", err)
		}
	}
	return nil
}

// allowAuthenticatedUsersToStart replaces the service DACL with the standard
// service DACL plus SERVICE_START/SERVICE_QUERY_STATUS for authenticated
// users. This is what lets the non-elevated GUI start the daemon silently.
func allowAuthenticatedUsersToStart(s *mgr.Service) error {
	const sddl = `D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)(A;;LCRP;;;AU)`
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(s.Handle, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func uninstallWindowsService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(windowsServiceName)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return err
	}
	defer s.Close()
	stopWindowsService(s)
	if err := s.Delete(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return fmt.Errorf("delete service: %w", err)
	}
	return nil
}

func stopWindowsService(s *mgr.Service) {
	st, err := s.Query()
	if err != nil || st.State == svc.Stopped {
		return
	}
	_, _ = s.Control(svc.Stop)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st, err = s.Query()
		if err != nil || st.State == svc.Stopped {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
