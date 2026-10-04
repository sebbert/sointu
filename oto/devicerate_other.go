//go:build !darwin && !windows && (!linux || android)

package oto

// deviceRate returns 0: the rate of the device is not known here.
func deviceRate() int { return 0 }
