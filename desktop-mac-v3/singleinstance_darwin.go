//go:build darwin

package main

import (
	"log"
	"os"
	"path/filepath"
	"syscall"
)

// acquireSingleInstance prevents running two copies of the binary directly
// (LaunchServices normally re-activates the existing .app, but a terminal or
// script can still start a second process).
func acquireSingleInstance() (func(), bool) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return func() {}, true
	}
	dir = filepath.Join(dir, "SNET")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("single instance dir: %v", err)
		return func() {}, true
	}
	f, err := os.OpenFile(filepath.Join(dir, "Snet.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Printf("single instance lock: %v", err)
		return func() {}, true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, false
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true
}
