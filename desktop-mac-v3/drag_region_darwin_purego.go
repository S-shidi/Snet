//go:build darwin && !cgo

package main

import "unsafe"

// installMacDragRegion is a no-op for pure-Go builds. The shared web UI marks
// its header as a Wails drag region, which drives NSWindow through the Wails
// runtime without registering a custom Objective-C view. The shipped macOS
// build uses cgo and the AppKit overlay in drag_region_darwin.go.
func installMacDragRegion(unsafe.Pointer) {}
