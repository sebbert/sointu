//go:build !darwin

package plugin

// onWindowThread tells if the caller runs on the thread that windows are
// made and closed on. Only macOS has one such thread for all windows.
func onWindowThread() bool { return false }
