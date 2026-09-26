# Build sointu's command line tools, tracker and VST2 and CLAP plugins.
#
#   make                      build everything into out/
#   make compile track play   build individual tools
#   make vst clap             build the VST2 or CLAP plugin
#   make install-vst          install a plugin into the user plugin directory
#   make install-clap
#   sudo make install-vst-system
#   sudo make install-clap-system
#                             install a plugin into the system plugin directory
#   make uninstall-vst        remove a plugin from the user plugin directory
#   make uninstall-clap
#   make clean
#
# The VST2 plugin is a .vst bundle on macOS, a .so on Linux and a .dll on
# Windows. The CLAP plugin is a .clap bundle on macOS and a .clap shared
# library elsewhere. On Windows, run from a POSIX shell such as Git Bash or
# MSYS2.
#
# Set VST_DIR=... or CLAP_DIR=... to install a plugin somewhere else. Set
# NATIVE=1 to use the native synth in the tracker and plugins (x86 only;
# requires the native library to be built into build/ with CMake first).

GO      ?= go
OUT     := out
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
vst_FILE        := sointu-vsti.vst
vst_USER_DIR    := $(HOME)/Library/Audio/Plug-Ins/VST
vst_SYSTEM_DIR  := /Library/Audio/Plug-Ins/VST
clap_USER_DIR   := $(HOME)/Library/Audio/Plug-Ins/CLAP
clap_SYSTEM_DIR := /Library/Audio/Plug-Ins/CLAP
else ifeq ($(PLATFORM),linux)
vst_FILE        := sointu-vsti.so
vst_USER_DIR    := $(HOME)/.vst
vst_SYSTEM_DIR  := /usr/local/lib/vst
clap_USER_DIR   := $(HOME)/.clap
clap_SYSTEM_DIR := /usr/lib/clap
else
EXE             := .exe
vst_FILE        := sointu-vsti.dll
vst_USER_DIR    := # Windows has no common per-user VST directory; set VST_DIR
vst_SYSTEM_DIR  := C:/Program Files/Common Files/VST2
clap_USER_DIR   := $(subst \,/,$(LOCALAPPDATA))/Programs/Common/CLAP
clap_SYSTEM_DIR := C:/Program Files/Common Files/CLAP
endif
clap_FILE := sointu-clap.clap
vst_DIR   := $(VST_DIR)
clap_DIR  := $(CLAP_DIR)
vst_PKG   := sointu-vsti
clap_PKG  := sointu-clap

TOOLS   := compile track play
PLUGINS := vst clap

.PHONY: all $(TOOLS) $(PLUGINS) clean FORCE \
	$(foreach p,$(PLUGINS),install-$(p) install-$(p)-system uninstall-$(p) uninstall-$(p)-system)

all: $(TOOLS) $(PLUGINS)

$(TOOLS): %: $(OUT)/sointu-%$(EXE)

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

# plugin-rules generates the build, install and uninstall rules for plugin $(1).
#
# Plugin hosts on macOS only load bundles. The bundle is signed ad-hoc as a
# whole; some hosts (e.g. Ableton Live) reject bundles carrying only the
# linker's signature on the binary.
#
# Installing replaces rather than overwrites in place, so a host that has the
# old binary mapped keeps running on the old inode instead of crashing. The
# system install does not depend on the build, so the build is not done as
# root; build first.
define plugin-rules
$(1): $(OUT)/$($(1)_FILE)

ifeq ($(PLATFORM),macos)
$(OUT)/$($(1)_FILE): FORCE
	rm -rf $$@
	mkdir -p $$@/Contents/MacOS
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(PLUGIN_TAGS) \
		-ldflags "$(LDFLAGS)" -o $$@/Contents/MacOS/$($(1)_PKG) ./cmd/$($(1)_PKG)/
	rm -f $$@/Contents/MacOS/$($(1)_PKG).h
	sed 's/@VERSION@/$(VERSION)/g' cmd/$($(1)_PKG)/Info.plist > $$@/Contents/Info.plist
	printf 'BNDL????' > $$@/Contents/PkgInfo
	codesign --force --sign - $$@
else
$(OUT)/$($(1)_FILE): FORCE
	mkdir -p $(OUT)
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(PLUGIN_TAGS) \
		-ldflags "$(LDFLAGS)" -o $$@ ./cmd/$($(1)_PKG)/
	rm -f $(OUT)/$(basename $($(1)_FILE)).h
endif

install-$(1): $(1)
	$$(call install-plugin,$(1),$$(or $($(1)_DIR),$($(1)_USER_DIR)))

install-$(1)-system:
	@test -e $(OUT)/$($(1)_FILE) || { echo "$(OUT)/$($(1)_FILE) missing; run 'make $(1)' first" >&2; exit 1; }
	$$(call install-plugin,$(1),$$(or $($(1)_DIR),$($(1)_SYSTEM_DIR)))

uninstall-$(1):
	$$(call uninstall-plugin,$(1),$$(or $($(1)_DIR),$($(1)_USER_DIR)))

uninstall-$(1)-system:
	$$(call uninstall-plugin,$(1),$$(or $($(1)_DIR),$($(1)_SYSTEM_DIR)))
endef

define check-dir
	@test -n "$(2)" || { echo "no default $(1) directory on $(PLATFORM); set $(shell echo $(1) | tr a-z A-Z)_DIR" >&2; exit 1; }
endef

define install-plugin
	$(call check-dir,$(1),$(2))
	mkdir -p "$(2)"
	rm -rf "$(2)/$($(1)_FILE)"
	cp -R $(OUT)/$($(1)_FILE) "$(2)/$($(1)_FILE)"
endef

define uninstall-plugin
	$(call check-dir,$(1),$(2))
	rm -rf "$(2)/$($(1)_FILE)"
endef

$(foreach p,$(PLUGINS),$(eval $(call plugin-rules,$(p))))

clean:
	rm -rf $(OUT)
