//go:build plugin

// Command sointu-clap is the Sointu CLAP plugin. The CLAP ABI is implemented in
// clap.c, which calls the Go side through cmd/plugin/engine (engine.h).
package main

/*
#cgo CFLAGS: -I${SRCDIR}/../../third_party/clap/include -I${SRCDIR}/../plugin/engine
*/
import "C"

import _ "github.com/vsariola/sointu/cmd/plugin/engine"

func main() {}
