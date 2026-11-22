//go:build !windows

package main

import (
	"fmt"
	"os"
)

func handleServiceCommand(args []string) (handled bool, exitCode int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "install", "uninstall":
		fmt.Fprintln(os.Stderr, "Windows service commands are only available on Windows")
		return true, 2
	}
	return false, 0
}
