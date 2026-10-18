# Build sointu's command line tools, tracker and VST2, VST3, CLAP and Audio
# Unit plugins.
#
#   make                      build everything into out/
#   make compile track play mcp
#                             build individual tools
#   make vst vst3 clap        build the VST2, VST3 or CLAP plugin
#   make au                   build the Audio Unit plugin (macOS only)
#   make install-vst          install a plugin into the user plugin directory
#   make install-vst3
#   make install-clap
#   make install-au
#   sudo make install-vst-system
#   sudo make install-clap-system
#                             install a plugin into the system plugin directory
#   make uninstall-vst        remove a plugin from the user plugin directory
#   make uninstall-vst3
#   make uninstall-clap
#   make clean
#
# The dev loader, for developing Sointu (macOS only): plugins whose Go side
# can be rebuilt and reloaded while the host runs. See FORK.md.
#
#   make install-dev-loader   install the loader as the VST3, CLAP and AU
#                             plugins; restart the host once after it
#   make install-engine       build the Go side and install it; the plugins
#                             then offer to reload (Edit > Reload plugin)
#
# The VST2 plugin is a .vst bundle on macOS, a .so on Linux and a .dll on
# Windows. The VST3 plugin is a .vst3 bundle everywhere, with the library in
# Contents/MacOS, Contents/<arch>-linux or Contents/x86_64-win. The CLAP plugin is a .clap bundle on macOS and a .clap shared
# library elsewhere. The Audio Unit is a .component bundle. On Windows, run from a POSIX shell such as Git Bash or
# MSYS2.
#
# Set VST_DIR=..., VST3_DIR=..., CLAP_DIR=... or AU_DIR=... to install a plugin somewhere else. Set
# NATIVE=1 to use the native synth in the tracker and plugins (x86 only;
# requires the native library to be built into build/ with CMake first).
#
# Set REX=1 to read REX files (ReCycle loops) in the tracker and the plugins,
# with the REX Shared Library of Reason Studios (macOS only): its SDK is
# downloaded into .rexsdk/ and the library is put into out/ next to the
# tracker and into each plugin bundle, with the SDK's license. Set
# REXSDK=... to use an SDK unpacked elsewhere instead. Such builds are for
# yourself: the SDK's license does not allow them to be published.

GO      ?= go
OUT     := out
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS := -X github.com/vsariola/sointu/version.Version=$(VERSION)
NATIVE_TAGS := $(if $(NATIVE),native)
REXSDK_URL  := https://cdn.reasonstudios.com/developers/RexSDK/1/192/REXSDK_Mac_1.9.2.zip
REXSDK      ?= $(if $(REX),.rexsdk)
REX_TAGS    := $(if $(REXSDK),rex)
REX_ENV     := $(if $(REXSDK),CGO_CFLAGS="-g -O2 -I$(abspath $(REXSDK))")
REX_DEP     := $(if $(REXSDK),$(REXSDK)/REX.h)
REX_BUNDLE  := $(REXSDK)/Mac/Deployment/REX Shared Library.bundle
REX_LICENSE := $(REXSDK)/General License Agreement.txt

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
vst3_USER_DIR   := $(HOME)/Library/Audio/Plug-Ins/VST3
vst3_SYSTEM_DIR := /Library/Audio/Plug-Ins/VST3
au_FILE         := sointu-au.component
au_USER_DIR     := $(HOME)/Library/Audio/Plug-Ins/Components
au_SYSTEM_DIR   := /Library/Audio/Plug-Ins/Components
else ifeq ($(PLATFORM),linux)
vst_FILE        := sointu-vsti.so
vst_USER_DIR    := $(HOME)/.vst
vst_SYSTEM_DIR  := /usr/local/lib/vst
clap_USER_DIR   := $(HOME)/.clap
clap_SYSTEM_DIR := /usr/lib/clap
vst3_USER_DIR   := $(HOME)/.vst3
vst3_SYSTEM_DIR := /usr/lib/vst3
vst3_BIN        := Contents/$(shell uname -m)-linux/sointu-vst3.so
else
EXE             := .exe
vst_FILE        := sointu-vsti.dll
vst_USER_DIR    := # Windows has no common per-user VST directory; set VST_DIR
vst_SYSTEM_DIR  := C:/Program Files/Common Files/VST2
clap_USER_DIR   := $(subst \,/,$(LOCALAPPDATA))/Programs/Common/CLAP
clap_SYSTEM_DIR := C:/Program Files/Common Files/CLAP
vst3_USER_DIR   := $(subst \,/,$(LOCALAPPDATA))/Programs/Common/VST3
vst3_SYSTEM_DIR := C:/Program Files/Common Files/VST3
vst3_BIN        := Contents/x86_64-win/sointu-vst3.vst3
endif
clap_FILE := sointu-clap.clap
vst3_FILE := sointu-vst3.vst3
vst_DIR   := $(VST_DIR)
vst3_DIR  := $(VST3_DIR)
clap_DIR  := $(CLAP_DIR)
au_DIR    := $(AU_DIR)
vst_PKG   := sointu-vsti
clap_PKG  := sointu-clap
vst3_PKG  := sointu-vst3
au_PKG    := sointu-au

TOOLS   := compile track play mcp
PLUGINS := vst vst3 clap
ifeq ($(PLATFORM),macos)
PLUGINS += au
endif

.PHONY: all $(TOOLS) $(PLUGINS) clean FORCE \
	$(foreach p,$(PLUGINS),install-$(p) install-$(p)-system uninstall-$(p) uninstall-$(p)-system)

all: $(TOOLS) $(PLUGINS)

$(TOOLS): %: $(OUT)/sointu-%$(EXE)

# Everything depends on FORCE and is always rebuilt: go build does its own
# caching.
$(OUT)/sointu-track$(EXE): TAGS := $(NATIVE_TAGS) $(REX_TAGS)
$(OUT)/sointu-track$(EXE): $(REX_DEP)
ifeq ($(PLATFORM),windows)
$(OUT)/sointu-track$(EXE): EXTRA_LDFLAGS := -H=windowsgui
endif

$(OUT)/sointu-%$(EXE): FORCE
	mkdir -p $(OUT)
	$(if $(filter rex,$(TAGS)),$(REX_ENV)) $(GO) build -tags=$(call join-tags,$(TAGS)) \
		-ldflags "$(LDFLAGS) $(EXTRA_LDFLAGS)" -o $@ ./cmd/sointu-$*/
ifneq ($(REXSDK),)
	$(if $(filter rex,$(TAGS)),rm -rf "$(OUT)/REX Shared Library.bundle" && cp -R "$(REX_BUNDLE)" $(OUT)/ && cp "$(REX_LICENSE)" "$(OUT)/REX License Agreement.txt")
endif

# the REX SDK, downloaded under its license, when REX=1
.rexsdk/REX.h:
	mkdir -p .rexsdk
	curl -fL -o .rexsdk/sdk.zip $(REXSDK_URL)
	unzip -q -o .rexsdk/sdk.zip -d .rexsdk
	rm .rexsdk/sdk.zip
	touch $@

PLUGIN_TAGS := $(call join-tags,plugin $(NATIVE_TAGS) $(REX_TAGS))

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
$(OUT)/$($(1)_FILE): FORCE $(REX_DEP)
	rm -rf $$@
	mkdir -p $$@/Contents/MacOS
	CGO_ENABLED=1 $(REX_ENV) $(GO) build -buildmode=c-shared -tags=$(PLUGIN_TAGS) \
		-ldflags "$(LDFLAGS)" -o $$@/Contents/MacOS/$($(1)_PKG) ./cmd/$($(1)_PKG)/
	rm -f $$@/Contents/MacOS/$($(1)_PKG).h
ifneq ($(REXSDK),)
	mkdir -p $$@/Contents/Frameworks $$@/Contents/Resources
	cp -R "$(REX_BUNDLE)" $$@/Contents/Frameworks/
	cp "$(REX_LICENSE)" "$$@/Contents/Resources/REX License Agreement.txt"
endif
	sed 's/@VERSION@/$(VERSION)/g' cmd/$($(1)_PKG)/Info.plist > $$@/Contents/Info.plist
	printf 'BNDL????' > $$@/Contents/PkgInfo
	codesign --force --sign - $$@
else ifneq ($($(1)_BIN),)
$(OUT)/$($(1)_FILE): FORCE
	rm -rf $$@
	mkdir -p $$(dir $$@/$($(1)_BIN))
	CGO_ENABLED=1 $(GO) build -buildmode=c-shared -tags=$(PLUGIN_TAGS) \
		-ldflags "$(LDFLAGS)" -o $$@/$($(1)_BIN) ./cmd/$($(1)_PKG)/
	rm -f $$(basename $$@/$($(1)_BIN)).h
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

# The dev loader: the C layer of a plugin with cmd/plugin/loader instead of
# the Go side, which it loads from ENGINE_DIR: the engine, built alone
# (cmd/sointu-engine). make install-engine installs each build under a name
# of its own and writes the name to the file current, which the loader
# watches; the newest five builds are kept. The bundles of the loader keep
# the names and IDs of the plugins, so that projects open with either, and
# are only installed again when their C code changes.
ifeq ($(PLATFORM),macos)
ENGINE_DIR     ?= $(HOME)/Library/Application Support/sointu/engine
LOADER_OUT     := $(OUT)/dev-loader
LOADER_CFLAGS  := -O2 -Wall -Wno-unused-function -Icmd/plugin/engine -DSOINTU_LOADER_VERSION='"$(VERSION)-loader"'
vst3_LOADER    := cmd/sointu-vst3/vst3.c cmd/sointu-vst3/view_darwin.m -Ithird_party/vst3 -framework Cocoa -framework CoreFoundation
clap_LOADER    := cmd/sointu-clap/clap.c -Ithird_party/clap/include
au_LOADER      := cmd/sointu-au/au_darwin.m -framework AudioToolbox -framework CoreFoundation -framework Cocoa
LOADER_PLUGINS := vst3 clap au

.PHONY: dev-loader install-dev-loader engine install-engine \
	$(foreach p,$(LOADER_PLUGINS),dev-loader-$(p) install-dev-loader-$(p))

dev-loader: $(foreach p,$(LOADER_PLUGINS),dev-loader-$(p))
install-dev-loader: $(foreach p,$(LOADER_PLUGINS),install-dev-loader-$(p))

define loader-rules
dev-loader-$(1): $(LOADER_OUT)/$($(1)_FILE)

$(LOADER_OUT)/$($(1)_FILE): FORCE
	rm -rf $$@
	mkdir -p $$@/Contents/MacOS
	clang -dynamiclib $(LOADER_CFLAGS) -o $$@/Contents/MacOS/$($(1)_PKG) cmd/plugin/loader/loader.c $($(1)_LOADER)
	sed 's/@VERSION@/$(VERSION)/g' cmd/$($(1)_PKG)/Info.plist > $$@/Contents/Info.plist
	printf 'BNDL????' > $$@/Contents/PkgInfo
	codesign --force --sign - $$@

install-dev-loader-$(1): dev-loader-$(1)
	mkdir -p "$$(or $($(1)_DIR),$($(1)_USER_DIR))"
	rm -rf "$$(or $($(1)_DIR),$($(1)_USER_DIR))/$($(1)_FILE)"
	cp -R $(LOADER_OUT)/$($(1)_FILE) "$$(or $($(1)_DIR),$($(1)_USER_DIR))/$($(1)_FILE)"
endef

$(foreach p,$(LOADER_PLUGINS),$(eval $(call loader-rules,$(p))))

engine: $(OUT)/sointu-engine.dylib

$(OUT)/sointu-engine.dylib: FORCE $(REX_DEP)
	mkdir -p $(OUT)
	CGO_ENABLED=1 $(REX_ENV) $(GO) build -buildmode=c-shared -tags=$(PLUGIN_TAGS) \
		-ldflags "$(LDFLAGS)" -o $@ ./cmd/sointu-engine/
	rm -f $(OUT)/sointu-engine.h
	codesign --force --sign - $@

install-engine: engine
	mkdir -p "$(ENGINE_DIR)"
ifneq ($(REXSDK),)
	test -d "$(ENGINE_DIR)/REX Shared Library.bundle" || cp -R "$(REX_BUNDLE)" "$(ENGINE_DIR)/"
	test -f "$(ENGINE_DIR)/REX License Agreement.txt" || cp "$(REX_LICENSE)" "$(ENGINE_DIR)/REX License Agreement.txt"
endif
	name=sointu-engine-$(VERSION)-$$(date +%Y%m%d-%H%M%S).dylib && \
	cp $(OUT)/sointu-engine.dylib "$(ENGINE_DIR)/$$name" && \
	printf '%s\n' "$$name" > "$(ENGINE_DIR)/current.tmp" && \
	mv "$(ENGINE_DIR)/current.tmp" "$(ENGINE_DIR)/current" && \
	echo "installed $$name"
	cd "$(ENGINE_DIR)" && ls -t sointu-engine-*.dylib | tail -n +6 | while read f; do rm -f "$$f"; done
endif

clean:
	rm -rf $(OUT)
