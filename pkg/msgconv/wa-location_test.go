package msgconv

import (
	"context"
	"strings"
	"testing"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"maunium.net/go/mautrix/event"
)

func TestLiveLocationIsItsStartingPoint(t *testing.T) {
	mc := &MessageConverter{}
	part, _ := mc.convertLiveLocationMessage(context.Background(), &waE2E.LiveLocationMessage{
		DegreesLatitude:  ptr.Ptr(52.5),
		DegreesLongitude: ptr.Ptr(-13.4),
		Caption:          ptr.Ptr("on my way"),
	})
	if part.Content.MsgType != event.MsgLocation {
		t.Fatalf("msgtype %q, want a location", part.Content.MsgType)
	}
	if part.Content.GeoURI != "geo:52.50000,-13.40000" {
		t.Errorf("geo URI %q", part.Content.GeoURI)
	}
	if !strings.HasPrefix(part.Content.Body, "on my way\n") || !strings.Contains(part.Content.Body, "Live location") {
		t.Errorf("body %q", part.Content.Body)
	}
}
