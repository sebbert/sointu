//go:build plugin

// Command sointu-engine is the Go side of the VST3, CLAP and Audio Unit
// plugins alone, as a library that the dev loader (cmd/plugin/loader) loads:
// the C interface of cmd/plugin/engine/engine.h. make install-engine builds
// and installs it; see the dev loader in FORK.md.
package main

import _ "github.com/vsariola/sointu/cmd/plugin/engine"

func main() {}
