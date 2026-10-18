//go:build plugin && darwin

// Command sointu-au is the Sointu Audio Unit (version 2) plugin, for macOS.
// The Audio Unit ABI is implemented in au_darwin.m, which calls the Go side
// through cmd/plugin/engine (engine.h).
package main

/*
#cgo CFLAGS: -I${SRCDIR}/../plugin/engine
#cgo LDFLAGS: -framework AudioToolbox -framework CoreFoundation -framework Cocoa
*/
import "C"

import _ "github.com/vsariola/sointu/cmd/plugin/engine"

func main() {}
