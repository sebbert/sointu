# macOS VST2 plugin bundle for sointu-vsti.
#
#   make vst                  build out/sointu-vsti.vst
#   make install-vst          install into ~/Library/Audio/Plug-Ins/VST
#   sudo make install-vst-system
#                             install into /Library/Audio/Plug-Ins/VST
#   make uninstall-vst        remove from ~/Library/Audio/Plug-Ins/VST
#   make clean
#
# Set GOTAGS=plugin,native to use the native synth (requires the native
# library to be built into build/ with CMake first).

GO      ?= go
GOTAGS  ?= plugin
OUT     := out
NAME    := sointu-vsti
BUNDLE  := $(OUT)/$(NAME).vst
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null)

USER_VST_DIR   := $(HOME)/Library/Audio/Plug-Ins/VST
SYSTEM_VST_DIR := /Library/Audio/Plug-Ins/VST

.PHONY: vst install-vst install-vst-system uninstall-vst uninstall-vst-system clean

vst: $(BUNDLE)

# Always rebuild: go build does its own caching. The bundle is signed ad-hoc
# as a whole; some hosts (e.g. Ableton Live) reject bundles carrying only the
# linker's signature on the binary.
.PHONY: $(BUNDLE)
$(BUNDLE):
	rm -rf $@
	mkdir -p $@/Contents/MacOS
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(GOTAGS) \
		-ldflags "-X github.com/vsariola/sointu/version.Version=$(VERSION)" \
		-o $@/Contents/MacOS/$(NAME) ./cmd/sointu-vsti/
	rm -f $@/Contents/MacOS/$(NAME).h
	sed 's/@VERSION@/$(VERSION)/g' cmd/sointu-vsti/Info.plist > $@/Contents/Info.plist
	printf 'BNDL????' > $@/Contents/PkgInfo
	codesign --force --sign - $@

# Replace rather than overwrite in place, so a host that has the old binary
# mapped keeps running on the old inode instead of crashing.
define install_bundle
	mkdir -p $(1)
	rm -rf $(1)/$(NAME).vst
	ditto $(BUNDLE) $(1)/$(NAME).vst
endef

install-vst: vst
	$(call install_bundle,$(USER_VST_DIR))

# Run with sudo. Does not depend on vst, so the build is not done as root;
# run `make vst` first.
install-vst-system:
	@test -d $(BUNDLE) || { echo "$(BUNDLE) missing; run 'make vst' first" >&2; exit 1; }
	$(call install_bundle,$(SYSTEM_VST_DIR))

uninstall-vst:
	rm -rf $(USER_VST_DIR)/$(NAME).vst

uninstall-vst-system:
	rm -rf $(SYSTEM_VST_DIR)/$(NAME).vst

clean:
	rm -rf $(OUT)
