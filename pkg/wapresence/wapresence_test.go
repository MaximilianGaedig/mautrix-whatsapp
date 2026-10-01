// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2026 Maximilian Gaedig
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package wapresence

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

var (
	now    = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	alice  = types.NewJID("111", types.DefaultUserServer)
	bob    = types.NewJID("222", types.HiddenUserServer)
	carol  = types.NewJID("333", types.HiddenUserServer)
	dave   = types.NewJID("444", types.HiddenUserServer)
	me     = types.NewJID("999", types.HiddenUserServer)
	group  = types.NewJID("120363000000000001", types.GroupServer)
	letter = types.NewJID("120363000000000002", types.NewsletterServer)
)

func source(chat, sender types.JID, fromMe bool) types.MessageSource {
	return types.MessageSource{Chat: chat, Sender: sender, IsFromMe: fromMe, IsGroup: chat.Server == types.GroupServer}
}

func TestActivityOf(t *testing.T) {
	sent := now.Add(-3 * time.Second)
	tests := []struct {
		name string
		evt  any
		who  types.JID
		at   time.Time
	}{
		{"a message in a chat", &events.Message{Info: types.MessageInfo{MessageSource: source(alice, alice, false), Timestamp: sent}}, alice, sent},
		{"a message in a group is the member's", &events.Message{Info: types.MessageInfo{MessageSource: source(group, bob, false), Timestamp: sent}}, bob, sent},
		{"our own message", &events.Message{Info: types.MessageInfo{MessageSource: source(group, me, true), Timestamp: sent}}, types.EmptyJID, time.Time{}},
		{"a newsletter post is nobody's", &events.Message{Info: types.MessageInfo{MessageSource: source(letter, letter, false), Timestamp: sent}}, types.EmptyJID, time.Time{}},
		{"a message we could not decrypt was still sent", &events.UndecryptableMessage{Info: types.MessageInfo{MessageSource: source(group, bob, false), Timestamp: sent}}, bob, sent},
		{"typing in a chat", &events.ChatPresence{MessageSource: source(alice, alice, false), State: types.ChatPresenceComposing}, alice, now},
		{"typing in a group is the member's", &events.ChatPresence{MessageSource: source(group, carol, false), State: types.ChatPresenceComposing}, carol, now},
		{"stopping typing", &events.ChatPresence{MessageSource: source(alice, alice, false), State: types.ChatPresencePaused}, alice, now},
		{"reading our message", &events.Receipt{MessageSource: source(alice, alice, false), Type: types.ReceiptTypeRead, Timestamp: sent}, alice, sent},
		{"reading our message in a group", &events.Receipt{MessageSource: source(group, carol, false), Type: types.ReceiptTypeRead, Timestamp: sent}, carol, sent},
		{"playing our voice message", &events.Receipt{MessageSource: source(alice, alice, false), Type: types.ReceiptTypePlayed, Timestamp: sent}, alice, sent},
		{"a delivery is the phone, not the person", &events.Receipt{MessageSource: source(alice, alice, false), Type: types.ReceiptTypeDelivered, Timestamp: sent}, types.EmptyJID, time.Time{}},
		{"our other device reading", &events.Receipt{MessageSource: source(alice, me, true), Type: types.ReceiptTypeReadSelf, Timestamp: sent}, types.EmptyJID, time.Time{}},
		{"presence itself is not activity", &events.Presence{From: alice}, types.EmptyJID, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			who, at, ok := ActivityOf(tt.evt, now)
			if ok != !tt.who.IsEmpty() {
				t.Fatalf("ok = %v, got %v at %v", ok, who, at)
			}
			if ok && (who != tt.who || !at.Equal(tt.at)) {
				t.Errorf("got %v at %v, want %v at %v", who, at, tt.who, tt.at)
			}
		})
	}
}

func TestLastSeen(t *testing.T) {
	seen := now.Add(-2 * time.Hour)
	if at, ok := LastSeen(&events.Presence{From: alice, Unavailable: true, LastSeen: seen}); !ok || !at.Equal(seen) {
		t.Errorf("gone with a last seen: got %v, %v", at, ok)
	}
	// Someone who hides their last seen goes unavailable with no time: there is nothing to report,
	// and "now" would be made up.
	if at, ok := LastSeen(&events.Presence{From: alice, Unavailable: true}); ok {
		t.Errorf("gone with last seen hidden: got %v", at)
	}
	// Online is said through presence; a last seen next to it would be an older one.
	if at, ok := LastSeen(&events.Presence{From: alice, LastSeen: seen}); ok {
		t.Errorf("online: got %v", at)
	}
}

func TestOwnPresence(t *testing.T) {
	for _, wanted := range []types.Presence{types.PresenceAvailable, types.PresenceUnavailable} {
		if got := OwnPresence(false, wanted); got != wanted {
			t.Errorf("without presence bridging %q became %q", wanted, got)
		}
		if got := OwnPresence(true, wanted); got != types.PresenceAvailable {
			t.Errorf("with presence bridging %q became %q: WhatsApp stops sending presence to an unavailable device", wanted, got)
		}
	}
}

func isMe(jid types.JID) bool { return jid.User == me.User }

func TestPlanDMsFirstThenGroupMembers(t *testing.T) {
	dms := []types.JID{alice, group, me, bob, alice}
	groups := [][]types.JID{
		{carol, bob, me, letter},
		{dave, carol, types.NewJID("555", types.BotServer)},
	}
	steps := Plan(dms, groups, isMe, 10, 10)
	want := []Step{
		{JID: alice},
		{JID: bob, Wait: DMInterval},
		{JID: carol, Member: true, Wait: MemberInterval},
		{JID: dave, Member: true, Wait: MemberInterval},
	}
	if len(steps) != len(want) {
		t.Fatalf("got %+v", steps)
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Errorf("step %d: got %+v, want %+v", i, steps[i], want[i])
		}
	}
}

func TestPlanWithoutGroups(t *testing.T) {
	steps := Plan([]types.JID{alice, bob}, nil, isMe, 10, 10)
	if len(steps) != 2 || steps[0].Member || steps[1].Member {
		t.Fatalf("got %+v", steps)
	}
}

func TestPlanCaps(t *testing.T) {
	var dms []types.JID
	var members []types.JID
	for i := 0; i < 50; i++ {
		dms = append(dms, types.NewJID("1"+string(rune('0'+i/10))+string(rune('0'+i%10)), types.DefaultUserServer))
		members = append(members, types.NewJID("2"+string(rune('0'+i/10))+string(rune('0'+i%10)), types.HiddenUserServer))
	}
	steps := Plan(dms, [][]types.JID{members[:30], members[20:]}, isMe, 5, 40)
	var nDMs, nMembers int
	for _, s := range steps {
		if s.Member {
			nMembers++
		} else {
			nDMs++
		}
	}
	if nDMs != 5 || nMembers != 40 {
		t.Fatalf("got %d chats and %d members, want 5 and 40", nDMs, nMembers)
	}
	// The most recently read chats and groups come first, so they are the ones the cap keeps.
	if steps[4].JID != dms[4] || steps[5].JID != members[0] || steps[44].JID != members[39] {
		t.Errorf("wrong ones kept: %v, %v, %v", steps[4].JID, steps[5].JID, steps[44].JID)
	}
	if steps := Plan(dms, [][]types.JID{members}, isMe, 5, 0); len(steps) != 5 {
		t.Errorf("no room for members, got %d steps", len(steps))
	}
}

// The point of the pacing: a few hundred members must never leave as one burst.
func TestPlanIsPaced(t *testing.T) {
	var members []types.JID
	for i := 0; i < 300; i++ {
		members = append(members, types.NewJID("7"+string(rune('0'+i/100))+string(rune('0'+i/10%10))+string(rune('0'+i%10)), types.HiddenUserServer))
	}
	steps := Plan([]types.JID{alice}, [][]types.JID{members}, isMe, 100, 1000)
	if len(steps) != 301 {
		t.Fatalf("got %d steps", len(steps))
	}
	var total time.Duration
	for i, s := range steps {
		if i > 0 && s.Wait < DMInterval {
			t.Fatalf("step %d goes out %v after the one before", i, s.Wait)
		}
		if s.Member && s.Wait < MemberInterval {
			t.Fatalf("member step %d goes out %v after the one before", i, s.Wait)
		}
		total += s.Wait
	}
	if total < 5*time.Minute {
		t.Errorf("300 members asked for within %v", total)
	}
}
