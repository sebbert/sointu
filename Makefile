# Build and install the sointu-vsti VST2 plugin.
#
#   make vst                  build the plugin into out/
#   make install-vst          install into the user VST directory
#   sudo make install-vst-system
#                             install into the system VST directory
#   make uninstall-vst        remove from the user VST directory
#   make clean
#
# The plugin is a .vst bundle on macOS, a .so on Linux and a .dll on Windows.
# On Windows, run from a POSIX shell such as Git Bash or MSYS2.
#
# Set VST_DIR=... to install somewhere else. Set GOTAGS=plugin,native to use
# the native synth (requires the native library to be built into build/ with
# CMake first; x86 only).

GO      ?= go
GOTAGS  ?= plugin
OUT     := out
NAME    := sointu-vsti
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS := -X github.com/vsariola/sointu/version.Version=$(VERSION)

ifeq ($(OS),Windows_NT)
PLATFORM := windows
else ifeq ($(shell uname -s),Darwin)
PLATFORM := macos
else
PLATFORM := linux
endif

ifeq ($(PLATFORM),macos)
PLUGIN_NAME    := $(NAME).vst
USER_VST_DIR   := $(HOME)/Library/Audio/Plug-Ins/VST
SYSTEM_VST_DIR := /Library/Audio/Plug-Ins/VST
else ifeq ($(PLATFORM),linux)
PLUGIN_NAME    := $(NAME).so
USER_VST_DIR   := $(HOME)/.vst
SYSTEM_VST_DIR := /usr/local/lib/vst
else
PLUGIN_NAME    := $(NAME).dll
USER_VST_DIR   := # Windows has no common per-user VST directory; set VST_DIR
SYSTEM_VST_DIR := C:/Program Files/Common Files/VST2
endif

PLUGIN := $(OUT)/$(PLUGIN_NAME)

.PHONY: vst install-vst install-vst-system uninstall-vst uninstall-vst-system clean

vst: $(PLUGIN)

# Always rebuild: go build does its own caching.
.PHONY: $(PLUGIN)

ifeq ($(PLATFORM),macos)
# VST hosts on macOS only load bundles. The bundle is signed ad-hoc as a
# whole; some hosts (e.g. Ableton Live) reject bundles carrying only the
# linker's signature on the binary.
$(PLUGIN):
	rm -rf $@
	mkdir -p $@/Contents/MacOS
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(GOTAGS) \
		-ldflags "$(LDFLAGS)" -o $@/Contents/MacOS/$(NAME) ./cmd/sointu-vsti/
	rm -f $@/Contents/MacOS/$(NAME).h
	sed 's/@VERSION@/$(VERSION)/g' cmd/sointu-vsti/Info.plist > $@/Contents/Info.plist
	printf 'BNDL????' > $@/Contents/PkgInfo
	codesign --force --sign - $@
else
$(PLUGIN):
	mkdir -p $(OUT)
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(GOTAGS) \
		-ldflags "$(LDFLAGS)" -o $@ ./cmd/sointu-vsti/
	rm -f $(OUT)/$(NAME).h
endif

# Replace rather than overwrite in place, so a host that has the old binary
# mapped keeps running on the old inode instead of crashing.
define install_plugin
	@test -n "$(1)" || { echo "no default VST directory on $(PLATFORM); set VST_DIR" >&2; exit 1; }
	mkdir -p "$(1)"
	rm -rf "$(1)/$(PLUGIN_NAME)"
	cp -R $(PLUGIN) "$(1)/$(PLUGIN_NAME)"
endef

install-vst: vst
	$(call install_plugin,$(or $(VST_DIR),$(USER_VST_DIR)))

# Run as root/administrator. Does not depend on vst, so the build is not done
# as root; run `make vst` first.
install-vst-system:
	@test -e $(PLUGIN) || { echo "$(PLUGIN) missing; run 'make vst' first" >&2; exit 1; }
	$(call install_plugin,$(or $(VST_DIR),$(SYSTEM_VST_DIR)))

define uninstall_plugin
	@test -n "$(1)" || { echo "no default VST directory on $(PLATFORM); set VST_DIR" >&2; exit 1; }
	rm -rf "$(1)/$(PLUGIN_NAME)"
endef

uninstall-vst:
	$(call uninstall_plugin,$(or $(VST_DIR),$(USER_VST_DIR)))

uninstall-vst-system:
	$(call uninstall_plugin,$(or $(VST_DIR),$(SYSTEM_VST_DIR)))

clean:
	rm -rf $(OUT)
