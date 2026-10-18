//go:build plugin

// Command sointu-vst3 is the Sointu VST3 plugin. The VST3 ABI is implemented
// in vst3.c against the C API of the VST3 SDK (third_party/vst3), and the
// view that the host shows in view_darwin.m; they call the Go side through
// cmd/plugin/engine (engine.h).
package main

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/vst3 -I${SRCDIR}/../plugin/engine
#cgo darwin LDFLAGS: -framework Cocoa -framework CoreFoundation
*/
import "C"

import _ "github.com/vsariola/sointu/cmd/plugin/engine"

func main() {}
