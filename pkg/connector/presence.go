// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2026 Tulir Asokan
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

package connector

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/presence"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
	"go.mau.fi/mautrix-whatsapp/pkg/wapresence"
)

// waOnlineTTL bounds how long an "available" presence is trusted without a
// follow-up. WhatsApp normally sends "unavailable" when the contact leaves,
// but subscriptions die with the websocket, so a missed event must not leave
// the ghost online forever.
const waOnlineTTL = 30 * time.Minute

// maxPresenceGroupMembers caps how many group members are subscribed to with
// presence_group_members. At wapresence.MemberInterval a full list takes a quarter of an hour.
const maxPresenceGroupMembers = 1000

// mapWAPresence converts a whatsmeow presence event to Matrix presence.
// WhatsApp only distinguishes online from offline; LastSeen is zero when the
// contact hides it, but Matrix can't carry a last-seen timestamp either way.
func mapWAPresence(evt *events.Presence, now time.Time) presence.State {
	if evt.Unavailable {
		return presence.State{Presence: event.PresenceOffline}
	}
	return presence.State{Presence: event.PresenceOnline, Until: now.Add(waOnlineTTL)}
}

func (wa *WhatsAppConnector) startPresence() {
	if !wa.Config.PresenceBridging {
		return
	}
	wa.presence = presence.NewManager(presence.Config{
		Refresh:       time.Duration(wa.Config.PresenceRefreshSeconds) * time.Second,
		RatePerSecond: wa.Config.PresenceMaxPerSecond,
	}, presence.GhostSender(wa.Bridge))
	log := wa.Bridge.Log.With().Str("component", "presence").Logger()
	go wa.presence.Run(log.WithContext(wa.Bridge.BackgroundCtx))
	if wa.Config.PresenceLastActive {
		wa.seen = presence.NewSeenReporter(presence.GhostSeenSender(wa.Bridge))
		go wa.seen.Run(log.WithContext(wa.Bridge.BackgroundCtx))
	}
}

// ownPresence is the presence the bridge announces for the user. WhatsApp only
// delivers other users' presence to online devices, so presence bridging has
// to keep the linked device available.
func (wa *WhatsAppClient) ownPresence() types.Presence {
	if wa.Main.presence != nil {
		return types.PresenceAvailable
	}
	return types.PresenceUnavailable
}

// noteActivity marks a user online for a while after they sent a message, read ours or typed
// (presence.Manager.Activity), under both their phone number and LID ghost.
func (wa *WhatsAppClient) noteActivity(ctx context.Context, rawEvt any) {
	if wa.Main.presence == nil {
		return
	}
	jid, at, ok := wapresence.ActivityOf(rawEvt, time.Now())
	if !ok || wa.IsOwnJID(jid) {
		return
	}
	wa.forEachGhostJID(ctx, jid, func(j types.JID) {
		wa.Main.presence.Activity(string(waid.MakeUserID(j)), at)
	})
}

// forEachGhostJID calls fn with the user's JID and its phone-number/LID counterpart, since ghosts
// may exist under either (the sender skips ghosts that don't exist).
func (wa *WhatsAppClient) forEachGhostJID(ctx context.Context, jid types.JID, fn func(types.JID)) {
	from := jid.ToNonAD()
	fn(from)
	var alt types.JID
	switch from.Server {
	case types.DefaultUserServer:
		alt, _ = wa.GetStore().LIDs.GetLIDForPN(ctx, from)
	case types.HiddenUserServer:
		alt, _ = wa.GetStore().LIDs.GetPNForLID(ctx, from)
	}
	if !alt.IsEmpty() {
		fn(alt.ToNonAD())
	}
}

func (wa *WhatsAppClient) handleWAPresence(ctx context.Context, evt *events.Presence) {
	if wa.Main.presence == nil || wa.IsOwnJID(evt.From) {
		return
	}
	st := mapWAPresence(evt, time.Now())
	// "Offline" is all Matrix presence can say of someone who has come and gone; when they were
	// last here goes to the homeserver's activity log, where it keeps one.
	lastSeen, hasLastSeen := wapresence.LastSeen(evt)
	// Ghosts may exist under either the phone number or the LID, update both
	// (the senders skip ghosts that don't exist).
	wa.forEachGhostJID(ctx, evt.From, func(j types.JID) {
		id := string(waid.MakeUserID(j))
		wa.Main.presence.Update(id, st)
		if hasLastSeen {
			wa.Main.seen.Note(id, lastSeen)
		}
	})
}

type presenceSubscriptions struct {
	lock sync.Mutex
	jids map[types.JID]struct{}
}

// reserve marks jid as subscribed if it isn't already and the cap allows it.
func (ps *presenceSubscriptions) reserve(jid types.JID, max int) bool {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	if ps.jids == nil {
		ps.jids = make(map[types.JID]struct{})
	}
	if _, ok := ps.jids[jid]; ok || len(ps.jids) >= max {
		return false
	}
	ps.jids[jid] = struct{}{}
	return true
}

func (ps *presenceSubscriptions) release(jid types.JID) {
	ps.lock.Lock()
	delete(ps.jids, jid)
	ps.lock.Unlock()
}

func (ps *presenceSubscriptions) has(jid types.JID) bool {
	ps.lock.Lock()
	defer ps.lock.Unlock()
	_, ok := ps.jids[jid]
	return ok
}

func (ps *presenceSubscriptions) reset() {
	ps.lock.Lock()
	ps.jids = nil
	ps.lock.Unlock()
}

func (wa *WhatsAppClient) subscribeChatPresence(ctx context.Context, chat types.JID) {
	if wa.Main.presence == nil || !wapresence.IsSubscribable(chat) || wa.IsOwnJID(chat) {
		return
	}
	chat = chat.ToNonAD()
	if wa.presenceMemberSubs.has(chat) || !wa.presenceSubs.reserve(chat, wa.Main.Config.PresenceMaxSubscriptions) {
		return
	}
	go func() {
		if err := wa.Client.SubscribePresence(ctx, chat); err != nil {
			wa.presenceSubs.release(chat)
			zerolog.Ctx(ctx).Debug().Err(err).Stringer("jid", chat).Msg("Failed to subscribe to presence")
		}
	}()
}

// subscribePresences subscribes to the most recently read DMs of this login after
// (re)connecting, and with presence_group_members to the members of its groups after them.
// Server-side subscriptions don't survive the websocket, so the local sets are reset first and a
// run still going from the connection before is stopped.
func (wa *WhatsAppClient) subscribePresences(ctx context.Context) {
	if wa.Main.presence == nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if stopOld := wa.stopPresenceSubs.Swap(&cancel); stopOld != nil {
		(*stopOld)()
	}
	wa.presenceSubs.reset()
	wa.presenceMemberSubs.reset()
	ups, err := wa.Main.Bridge.DB.UserPortal.GetAllForLogin(ctx, wa.UserLogin.UserLogin)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to get portals for presence subscriptions")
		return
	}
	slices.SortFunc(ups, func(a, b *database.UserPortal) int {
		return cmp.Compare(b.LastRead.UnixMilli(), a.LastRead.UnixMilli())
	})
	var dms []types.JID
	var groups [][]types.JID
	for _, up := range ups {
		jid, err := waid.ParsePortalID(up.Portal.ID)
		if err != nil {
			continue
		} else if wapresence.IsSubscribable(jid) {
			dms = append(dms, jid)
		} else if jid.Server == types.GroupServer && wa.Main.Config.PresenceGroupMembers {
			if members := wa.groupMemberJIDs(ctx, up); len(members) > 0 {
				groups = append(groups, members)
			}
		}
	}
	steps := wapresence.Plan(dms, groups, wa.IsOwnJID, wa.Main.Config.PresenceMaxSubscriptions, maxPresenceGroupMembers)
	zerolog.Ctx(ctx).Debug().Int("count", len(steps)).Int("groups", len(groups)).Msg("Subscribing to presences")
	for _, step := range steps {
		// Paced to avoid looking like a scraper, see wapresence.MemberInterval.
		select {
		case <-ctx.Done():
			return
		case <-time.After(step.Wait):
		}
		if !wa.IsLoggedIn() || !wa.Client.IsConnected() {
			return
		}
		subs, limit := &wa.presenceSubs, wa.Main.Config.PresenceMaxSubscriptions
		if step.Member {
			if wa.presenceSubs.has(step.JID) {
				continue // started a chat with us since the plan was made
			}
			subs, limit = &wa.presenceMemberSubs, maxPresenceGroupMembers
		}
		if !subs.reserve(step.JID, limit) {
			continue
		}
		if err := wa.Client.SubscribePresence(ctx, step.JID); err != nil {
			subs.release(step.JID)
			zerolog.Ctx(ctx).Debug().Err(err).Stringer("jid", step.JID).Msg("Failed to subscribe to presence")
		}
	}
}

// groupMemberJIDs returns the members of a group as its Matrix room shows them (the ghosts
// joined to it), in a stable order. The room is asked instead of WhatsApp so that finding out
// whom to subscribe to costs no request to WhatsApp at all.
func (wa *WhatsAppClient) groupMemberJIDs(ctx context.Context, up *database.UserPortal) []types.JID {
	portal, err := wa.Main.Bridge.GetExistingPortalByKey(ctx, up.Portal)
	if err != nil || portal == nil || portal.MXID == "" || portal.RoomType == database.RoomTypeDM {
		return nil
	}
	joined, err := wa.Main.Bridge.Matrix.GetMembers(ctx, portal.MXID)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Stringer("room_id", portal.MXID).Msg("Failed to get members for presence")
		return nil
	}
	var members []types.JID
	for userID, member := range joined {
		if member == nil || member.Membership != event.MembershipJoin {
			continue
		}
		if ghost, ok := wa.Main.Bridge.Matrix.ParseGhostMXID(userID); ok {
			members = append(members, waid.ParseUserID(ghost))
		}
	}
	slices.SortFunc(members, func(a, b types.JID) int {
		return cmp.Or(cmp.Compare(a.Server, b.Server), cmp.Compare(a.User, b.User))
	})
	return members
}
