package connector

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
)

func TestCrossedAdmin(t *testing.T) {
	for _, tc := range []struct {
		orig, new int
		want      int
	}{
		{0, adminPL, 1},
		{0, 100, 1},
		{adminPL, 0, -1},
		{superAdminPL, adminPL, 0}, // still an admin
		{0, adminPL - 1, 0},        // still not one
		{adminPL, adminPL, 0},
	} {
		got := crossedAdmin(&bridgev2.SinglePowerLevelChange{OrigLevel: tc.orig, NewLevel: tc.new, NewIsSet: true})
		if got != tc.want {
			t.Errorf("%d → %d: got %d, want %d", tc.orig, tc.new, got, tc.want)
		}
	}
	if crossedAdmin(nil) != 0 {
		t.Error("no change")
	}
}
