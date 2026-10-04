package oto

import "testing"

// TestDeviceSampleRate checks that asking for the rate of the device gives
// a rate, with or without a device.
func TestDeviceSampleRate(t *testing.T) {
	rate := DeviceSampleRate()
	t.Logf("the system tells %d Hz, DeviceSampleRate gives %d Hz", deviceRate(), rate)
	if rate < 8000 || rate > 768000 {
		t.Errorf("DeviceSampleRate() = %d", rate)
	}
}
