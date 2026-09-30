package waid

import (
	"testing"
	"time"
)

func TestSetPinExpiry(t *testing.T) {
	var pm PortalMetadata
	at := time.Unix(1_700_000_000, 0)

	if !pm.SetPinExpiry("msg", true, at) || pm.PinExpiry["msg"].Unix() != at.Unix() {
		t.Fatalf("pin not recorded: %v", pm.PinExpiry)
	}
	if pm.SetPinExpiry("msg", true, at) {
		t.Error("the same expiry again is no change")
	}
	later := at.Add(time.Hour)
	if !pm.SetPinExpiry("msg", true, later) || pm.PinExpiry["msg"].Unix() != later.Unix() {
		t.Error("re-pinning must move the expiry")
	}
	if !pm.SetPinExpiry("msg", false, time.Time{}) || len(pm.PinExpiry) != 0 {
		t.Errorf("unpinning must forget the expiry: %v", pm.PinExpiry)
	}
	if pm.SetPinExpiry("other", false, time.Time{}) {
		t.Error("unpinning an unknown pin is no change")
	}
	if pm.SetPinExpiry("forever", true, time.Time{}) || len(pm.PinExpiry) != 0 {
		t.Error("a pin without an expiry has nothing to record")
	}
}
