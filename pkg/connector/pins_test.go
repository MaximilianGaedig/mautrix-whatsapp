package connector

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestPinChange(t *testing.T) {
	group := types.NewJID("123456", types.GroupServer)
	author := types.NewJID("111", types.HiddenUserServer)
	pinner := types.NewJID("222", types.HiddenUserServer)
	key := &waCommon.MessageKey{
		RemoteJID:   ptr.Ptr(group.String()),
		ID:          ptr.Ptr("PINNED"),
		Participant: ptr.Ptr(author.String()),
	}
	// The pin names its target by key; the change must point at the message the bridge stored for it.
	target := msgconv.KeyToMessageID(context.Background(), nil, group, pinner, key)
	if want := waid.MakeMessageID(group, author, "PINNED"); target != want {
		t.Fatalf("target = %q, want %q", target, want)
	}

	for _, tc := range []struct {
		typ  waE2E.PinInChatMessage_Type
		want *bridgev2.PinChange
	}{
		{waE2E.PinInChatMessage_PIN_FOR_ALL, &bridgev2.PinChange{MessageID: target, Pinned: true}},
		{waE2E.PinInChatMessage_UNPIN_FOR_ALL, &bridgev2.PinChange{MessageID: target, Pinned: false}},
		{waE2E.PinInChatMessage_UNKNOWN_TYPE, nil},
	} {
		info := pinChange(&waE2E.PinInChatMessage{Key: key, Type: tc.typ.Enum()}, target)
		if tc.want == nil {
			if info != nil {
				t.Errorf("%s: got %+v, want no change", tc.typ, info)
			}
			continue
		}
		if info == nil || len(info.PinChanges) != 1 || info.PinChanges[0] != *tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.typ, info, *tc.want)
		}
		if info != nil && (info.Name != nil || info.PinnedMessages != nil) {
			t.Errorf("%s: a pin must not touch other chat info: %+v", tc.typ, info)
		}
	}
	if pinChange(&waE2E.PinInChatMessage{Type: waE2E.PinInChatMessage_PIN_FOR_ALL.Enum()}, "") != nil {
		t.Error("a pin without a target must be ignored")
	}
}

func TestPinInChatMessage(t *testing.T) {
	key := &waCommon.MessageKey{ID: ptr.Ptr("PINNED")}
	ts := time.UnixMilli(1_700_000_000_123)

	pin := pinInChatMessage(key, true, ts)
	if got := pin.GetPinInChatMessage(); got.GetType() != waE2E.PinInChatMessage_PIN_FOR_ALL ||
		got.GetKey() != key || got.GetSenderTimestampMS() != ts.UnixMilli() {
		t.Fatalf("pin = %+v", got)
	}
	// WhatsApp only accepts pins with a lifetime: 24 hours, 7 days or 30 days.
	if d := pin.GetMessageContextInfo().GetMessageAddOnDurationInSecs(); d != 30*24*60*60 {
		t.Errorf("pin duration = %d", d)
	}
	if pin.GetMessageContextInfo().GetMessageAddOnExpiryType() != waE2E.MessageContextInfo_STATIC {
		t.Error("pin expiry type not set")
	}

	unpin := pinInChatMessage(key, false, ts)
	if unpin.GetPinInChatMessage().GetType() != waE2E.PinInChatMessage_UNPIN_FOR_ALL {
		t.Fatalf("unpin = %+v", unpin.GetPinInChatMessage())
	}
	if unpin.GetMessageContextInfo() != nil {
		t.Error("an unpin carries no duration")
	}
}

func TestPinExpiry(t *testing.T) {
	ts := time.Unix(1_700_000_000, 0)
	if got := pinExpiry(ts, 7*24*60*60); !got.Equal(ts.Add(7 * 24 * time.Hour)) {
		t.Errorf("7-day pin expires at %v", got)
	}
	if !pinExpiry(ts, 0).IsZero() {
		t.Error("a pin without a duration doesn't expire")
	}
}

func TestSchedulePinExpiryArmsOnce(t *testing.T) {
	armed := 0
	orig := afterFunc
	afterFunc = func(time.Duration, func()) { armed++ }
	t.Cleanup(func() { afterFunc = orig })

	wa := &WhatsAppClient{}
	expiry := time.Now().Add(time.Hour)
	wa.schedulePinExpiry(networkid.PortalKey{}, "m", expiry)
	wa.schedulePinExpiry(networkid.PortalKey{}, "m", expiry) // a reconnect re-arming the same pin
	if armed != 1 {
		t.Errorf("%d timers armed for one pin", armed)
	}
	wa.schedulePinExpiry(networkid.PortalKey{}, "m", expiry.Add(time.Hour)) // re-pinned: a new expiry
	wa.schedulePinExpiry(networkid.PortalKey{}, "n", time.Time{})           // doesn't expire
	if armed != 2 {
		t.Errorf("%d timers armed, want 2", armed)
	}
}
