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

// Package wapresence holds the decisions of WhatsApp presence bridging that need no connection:
// whose presence to ask for and how fast, what counts as someone being active, and what the
// bridge's own account has to announce. It is kept apart from the connector so that it builds and
// tests without cgo.
package wapresence

import (
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// IsSubscribable reports whether a JID is a person, under their phone number or their LID. Only
// people have presence: groups, newsletters, broadcasts and bots do not.
func IsSubscribable(jid types.JID) bool {
	return jid.Server == types.DefaultUserServer || jid.Server == types.HiddenUserServer
}

// ActivityOf returns who did something and when, for the events that mean a person was at their
// phone just then: sending a message (in a chat or a group, decryptable or not), typing, and
// reading or playing one of ours. A delivery receipt is not among them, the phone sends those on
// its own. now stands in for the events WhatsApp does not timestamp.
func ActivityOf(rawEvt any, now time.Time) (who types.JID, at time.Time, ok bool) {
	var src *types.MessageSource
	switch evt := rawEvt.(type) {
	case *events.Message:
		src, at = &evt.Info.MessageSource, evt.Info.Timestamp
	case *events.UndecryptableMessage:
		src, at = &evt.Info.MessageSource, evt.Info.Timestamp
	case *events.ChatPresence:
		// "Paused" counts as much as "composing": they were there to stop.
		src, at = &evt.MessageSource, now
	case *events.Receipt:
		// The -self types are the user's own other devices.
		if evt.Type != types.ReceiptTypeRead && evt.Type != types.ReceiptTypePlayed {
			return types.EmptyJID, time.Time{}, false
		}
		src, at = &evt.MessageSource, evt.Timestamp
	default:
		return types.EmptyJID, time.Time{}, false
	}
	// Newsletters and broadcasts post under their own JID, which is not a person.
	if src.IsFromMe || !IsSubscribable(src.Sender) {
		return types.EmptyJID, time.Time{}, false
	}
	return src.Sender, at, true
}

// LastSeen returns when WhatsApp says a contact was last online, which it tells with the presence
// of someone who is not online now. A contact who hides their last seen comes without a time, and
// then there is nothing to say: Matrix presence already carries the "offline".
func LastSeen(evt *events.Presence) (time.Time, bool) {
	if !evt.Unavailable || evt.LastSeen.IsZero() {
		return time.Time{}, false
	}
	return evt.LastSeen, true
}

// OwnPresence is what the bridge's own device announces when the connector wants to announce
// wanted. WhatsApp sends other people's presence (and typing) only to a device that is itself
// available, and stops the moment it says otherwise, so while presence is bridged the device may
// never go unavailable, whatever the reason was.
func OwnPresence(bridging bool, wanted types.Presence) types.Presence {
	if bridging {
		return types.PresenceAvailable
	}
	return wanted
}

const (
	// DMInterval is the time between two subscriptions to chat partners.
	DMInterval = 250 * time.Millisecond
	// MemberInterval is the time between two subscriptions to group members. The official app
	// subscribes to one person at a time, as the user opens a chat or a profile; it never asks
	// for a whole group. One a second keeps a few hundred members from leaving as one burst of
	// stanzas, which is what a scraper looks like, and still covers 300 people in five minutes
	// after a reconnect.
	MemberInterval = time.Second
)

// Step is one presence subscription to send.
type Step struct {
	JID types.JID
	// Member is set for someone who is asked for as a group member, not as a chat partner.
	Member bool
	// Wait is how long to wait after the step before.
	Wait time.Duration
}

// Plan lists the presence subscriptions to send after connecting, in order and with their
// spacing. dms are the partners of the login's 1:1 chats and groups the members of its groups,
// both most recently read first, so the caps keep the people the user deals with most. Chat
// partners go first at DMInterval, then the group members who are not already among them at
// MemberInterval. Nobody is asked for twice, and never the user themself or a JID that is not a
// person.
func Plan(dms []types.JID, groups [][]types.JID, isOwn func(types.JID) bool, maxDMs, maxMembers int) []Step {
	var steps []Step
	planned := make(map[types.JID]struct{})
	add := func(jid types.JID, member bool, wait time.Duration) {
		jid = jid.ToNonAD()
		if _, dup := planned[jid]; dup || !IsSubscribable(jid) || isOwn(jid) {
			return
		}
		planned[jid] = struct{}{}
		if len(steps) == 0 {
			wait = 0
		}
		steps = append(steps, Step{JID: jid, Member: member, Wait: wait})
	}
	for _, jid := range dms {
		if len(steps) >= maxDMs {
			break
		}
		add(jid, false, DMInterval)
	}
	nDMs := len(steps)
	for _, members := range groups {
		for _, jid := range members {
			if len(steps)-nDMs >= maxMembers {
				return steps
			}
			add(jid, true, MemberInterval)
		}
	}
	return steps
}
