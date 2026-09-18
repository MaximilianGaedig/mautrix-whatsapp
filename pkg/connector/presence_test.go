package connector

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/event"
)

func TestMapWAPresence(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	jid := types.NewJID("123", types.DefaultUserServer)

	st := mapWAPresence(&events.Presence{From: jid}, now)
	if st.Presence != event.PresenceOnline || !st.Until.Equal(now.Add(waOnlineTTL)) {
		t.Fatalf("available: got %+v", st)
	}
	st = mapWAPresence(&events.Presence{From: jid, Unavailable: true, LastSeen: now.Add(-time.Minute)}, now)
	if st.Presence != event.PresenceOffline || !st.Until.IsZero() {
		t.Fatalf("unavailable with last seen: got %+v", st)
	}
	st = mapWAPresence(&events.Presence{From: jid, Unavailable: true}, now)
	if st.Presence != event.PresenceOffline {
		t.Fatalf("unavailable, last seen hidden: got %+v", st)
	}
}

func TestPresenceSubscriptionsCap(t *testing.T) {
	var ps presenceSubscriptions
	a := types.NewJID("1", types.DefaultUserServer)
	b := types.NewJID("2", types.HiddenUserServer)
	c := types.NewJID("3", types.DefaultUserServer)
	if !ps.reserve(a, 2) || ps.reserve(a, 2) {
		t.Fatal("first reserve should succeed, duplicate should not")
	}
	if !ps.reserve(b, 2) || ps.reserve(c, 2) {
		t.Fatal("cap of 2 not enforced")
	}
	ps.release(a)
	if !ps.reserve(c, 2) {
		t.Fatal("release should free a slot")
	}
	ps.reset()
	if !ps.reserve(a, 2) {
		t.Fatal("reset should clear subscriptions")
	}
	if isPresenceSubscribable(types.NewJID("1", types.GroupServer)) {
		t.Fatal("groups are not subscribable")
	}
}
