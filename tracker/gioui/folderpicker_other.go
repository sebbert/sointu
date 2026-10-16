//go:build !darwin

package gioui

// canChooseFolder tells whether chooseFolder opens a panel of the system:
// only on macOS for now.
const canChooseFolder = false

func chooseFolder(dir string, f func(path string)) { go f("") }
