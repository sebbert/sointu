package gioui

import (
	"fmt"

	"github.com/vsariola/sointu/tracker"
)

// A plugin that the dev loader loaded (cmd/plugin/loader) can move to a
// newer build while the host runs: the loader tells when one is installed
// (ReloadAvailable), and the tracker offers to reload, or reloads at once
// if the user chose so (AutoReload). Other trackers and plugins have no
// reloader and show none of this.
type reloadControl struct {
	// request asks the loader to reload; nil without the loader
	request func()
	// build is the newer build installed, "" if none
	build string
	// requested is true once a reload was asked for: the instance is about
	// to be replaced
	requested bool
}

// SetReloader tells that request moves the plugin to the newest build. Call
// it on the tracker's goroutine.
func (t *Tracker) SetReloader(request func()) {
	t.reload.request = request
	if t.reload.build != "" {
		t.ReloadAvailable(t.reload.build)
	}
}

// ReloadAvailable tells that another build of the plugin is installed. Call
// it on the tracker's goroutine.
func (t *Tracker) ReloadAvailable(build string) {
	t.reload.build = build
	if t.reload.request == nil || t.reload.requested {
		return
	}
	if t.preferences.Reload.Auto {
		t.Alerts().AddNamed("Reload", fmt.Sprintf("Reloading the plugin: build %s", build), tracker.Info)
		t.requestReload()
		return
	}
	t.Alerts().AddNamed("Reload", fmt.Sprintf("Build %s installed: Edit → Reload plugin", build), tracker.Info)
}

func (t *Tracker) requestReload() {
	t.reload.requested = true
	t.reload.request()
}

// CanReload tells if the plugin can be moved to a newer build: the menu
// items of it show only then.
func (t *Tracker) CanReload() bool { return t.reload.request != nil }

// ReloadPlugin moves the plugin to the newer build that is installed.
func (t *Tracker) ReloadPlugin() tracker.Action {
	return tracker.MakeFuncAction(t.requestReload, func() bool {
		return t.reload.request != nil && t.reload.build != "" && !t.reload.requested
	})
}

// AutoReload toggles reloading the plugin as soon as a newer build is
// installed, and saves it in the custom preferences.
func (t *Tracker) AutoReload() tracker.Bool { return tracker.MakeBool((*autoReload)(t)) }

type autoReload Tracker

func (t *autoReload) Enabled() bool { return t.reload.request != nil }
func (t *autoReload) Value() bool   { return t.preferences.Reload.Auto }
func (t *autoReload) SetValue(val bool) {
	t.preferences.Reload.Auto = val
	if err := WriteCustomConfig("preferences.yml", val, "reload", "auto"); err != nil {
		(*Tracker)(t).Alerts().Add(fmt.Sprintf("Could not save preferences: %v", err), tracker.Error)
	}
	if val && t.reload.build != "" {
		(*Tracker)(t).ReloadAvailable(t.reload.build)
	}
}
