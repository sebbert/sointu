# Build sointu's command line tools, tracker and VST2 plugin.
#
#   make                      build everything into out/
#   make compile track play   build individual tools
#   make vst                  build the VST plugin
#   make install-vst          install the plugin into the user VST directory
#   sudo make install-vst-system
#                             install the plugin into the system VST directory
#   make uninstall-vst        remove the plugin from the user VST directory
#   make clean
#
# The plugin is a .vst bundle on macOS, a .so on Linux and a .dll on Windows.
# On Windows, run from a POSIX shell such as Git Bash or MSYS2.
#
# Set VST_DIR=... to install the plugin somewhere else. Set NATIVE=1 to use the
# native synth in the tracker and plugin (x86 only; requires the native
# library to be built into build/ with CMake first).

GO      ?= go
OUT     := out
NAME    := sointu-vsti
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS := -X github.com/vsariola/sointu/version.Version=$(VERSION)
NATIVE_TAGS := $(if $(NATIVE),native)

empty :=
space := $(empty) $(empty)
comma := ,
join-tags = $(subst $(space),$(comma),$(strip $(1)))

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
EXE            := .exe
PLUGIN_NAME    := $(NAME).dll
USER_VST_DIR   := # Windows has no common per-user VST directory; set VST_DIR
SYSTEM_VST_DIR := C:/Program Files/Common Files/VST2
endif

PLUGIN := $(OUT)/$(PLUGIN_NAME)
TOOLS  := compile track play

.PHONY: all $(TOOLS) vst install-vst install-vst-system uninstall-vst uninstall-vst-system clean FORCE

all: $(TOOLS) vst

$(TOOLS): %: $(OUT)/sointu-%$(EXE)

vst: $(PLUGIN)

# Everything depends on FORCE and is always rebuilt: go build does its own
# caching.
$(OUT)/sointu-track$(EXE): TAGS := $(NATIVE_TAGS)
ifeq ($(PLATFORM),windows)
$(OUT)/sointu-track$(EXE): EXTRA_LDFLAGS := -H=windowsgui
endif

$(OUT)/sointu-%$(EXE): FORCE
	mkdir -p $(OUT)
	$(GO) build -tags=$(call join-tags,$(TAGS)) \
		-ldflags "$(LDFLAGS) $(EXTRA_LDFLAGS)" -o $@ ./cmd/sointu-$*/

PLUGIN_TAGS := $(call join-tags,plugin $(NATIVE_TAGS))

ifeq ($(PLATFORM),macos)
# VST hosts on macOS only load bundles. The bundle is signed ad-hoc as a
# whole; some hosts (e.g. Ableton Live) reject bundles carrying only the
# linker's signature on the binary.
$(PLUGIN): FORCE
	rm -rf $@
	mkdir -p $@/Contents/MacOS
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(PLUGIN_TAGS) \
		-ldflags "$(LDFLAGS)" -o $@/Contents/MacOS/$(NAME) ./cmd/sointu-vsti/
	rm -f $@/Contents/MacOS/$(NAME).h
	sed 's/@VERSION@/$(VERSION)/g' cmd/sointu-vsti/Info.plist > $@/Contents/Info.plist
	printf 'BNDL????' > $@/Contents/PkgInfo
	codesign --force --sign - $@
else
$(PLUGIN): FORCE
	mkdir -p $(OUT)
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(PLUGIN_TAGS) \
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
