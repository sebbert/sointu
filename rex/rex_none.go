//go:build !rex || !cgo

package rex

// Available reports whether REX files can be loaded.
func Available() bool { return false }

// Decode renders a REX file with the REX Shared Library.
func Decode(data []byte) (Loop, error) { return Loop{}, ErrUnavailable }
