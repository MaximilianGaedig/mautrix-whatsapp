package connector

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func capsFor(disableViewOnce bool, roomType database.RoomType, announcements bool) *event.RoomFeatures {
	wa := &WhatsAppClient{Main: &WhatsAppConnector{}}
	wa.Main.Config.DisableViewOnce = disableViewOnce
	return wa.GetCapabilities(context.Background(), &bridgev2.Portal{Portal: &database.Portal{
		RoomType: roomType,
		Metadata: &waid.PortalMetadata{CommunityAnnouncementGroup: announcements},
	}})
}

// The bridge framework rejects a media event carrying com.beeper.view_limited unless the room features list
// that exact limit for its message type, so view-once media only reaches the converter if this holds.
func TestViewOnceIsAdvertisedWhereWhatsAppHasIt(t *testing.T) {
	once := &event.BeeperViewLimitedMedia{Type: "count", Count: 1}
	for _, room := range []struct {
		roomType      database.RoomType
		announcements bool
	}{{database.RoomTypeDefault, false}, {database.RoomTypeDM, false}, {database.RoomTypeDefault, true}} {
		caps := capsFor(false, room.roomType, room.announcements)
		for msgType, feat := range caps.File {
			want := msgType == event.MsgImage || msgType == event.MsgVideo || msgType == event.CapMsgVoice
			if got := feat.SupportsViewLimitedType(once); got != want {
				t.Errorf("%s: %s view-once = %v, want %v", caps.ID, msgType, got, want)
			}
		}
		// disable_view_once turns the feature off in both directions, and clients tell the two sets of
		// features apart by their ID.
		disabled := capsFor(true, room.roomType, room.announcements)
		if disabled.ID == caps.ID {
			t.Errorf("%s: same ID with view-once disabled", caps.ID)
		}
		for msgType, feat := range disabled.File {
			if feat.SupportsViewLimitedType(once) {
				t.Errorf("%s: %s is view-once despite disable_view_once", disabled.ID, msgType)
			}
		}
	}
}
