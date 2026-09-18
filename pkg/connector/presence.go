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
)

// waOnlineTTL bounds how long an "available" presence is trusted without a
// follow-up. WhatsApp normally sends "unavailable" when the contact leaves,
// but subscriptions die with the websocket, so a missed event must not leave
// the ghost online forever.
const waOnlineTTL = 30 * time.Minute

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

func (wa *WhatsAppClient) handleWAPresence(ctx context.Context, evt *events.Presence) {
	if wa.Main.presence == nil || wa.IsOwnJID(evt.From) {
		return
	}
	st := mapWAPresence(evt, time.Now())
	from := evt.From.ToNonAD()
	wa.Main.presence.Update(string(waid.MakeUserID(from)), st)
	// Ghosts may exist under either the phone number or the LID, update both
	// (the sender skips ghosts that don't exist).
	var alt types.JID
	switch from.Server {
	case types.DefaultUserServer:
		alt, _ = wa.GetStore().LIDs.GetLIDForPN(ctx, from)
	case types.HiddenUserServer:
		alt, _ = wa.GetStore().LIDs.GetPNForLID(ctx, from)
	}
	if !alt.IsEmpty() {
		wa.Main.presence.Update(string(waid.MakeUserID(alt.ToNonAD())), st)
	}
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

func (ps *presenceSubscriptions) reset() {
	ps.lock.Lock()
	ps.jids = nil
	ps.lock.Unlock()
}

func isPresenceSubscribable(jid types.JID) bool {
	return jid.Server == types.DefaultUserServer || jid.Server == types.HiddenUserServer
}

func (wa *WhatsAppClient) subscribeChatPresence(ctx context.Context, chat types.JID) {
	if wa.Main.presence == nil || !isPresenceSubscribable(chat) || wa.IsOwnJID(chat) {
		return
	}
	chat = chat.ToNonAD()
	if !wa.presenceSubs.reserve(chat, wa.Main.Config.PresenceMaxSubscriptions) {
		return
	}
	go func() {
		if err := wa.Client.SubscribePresence(ctx, chat); err != nil {
			wa.presenceSubs.release(chat)
			zerolog.Ctx(ctx).Debug().Err(err).Stringer("jid", chat).Msg("Failed to subscribe to presence")
		}
	}()
}

// subscribeRecentDMPresences subscribes to the most recently read DMs of this
// login after (re)connecting. Server-side subscriptions don't survive the
// websocket, so the local set is reset first.
func (wa *WhatsAppClient) subscribeRecentDMPresences(ctx context.Context) {
	if wa.Main.presence == nil {
		return
	}
	wa.presenceSubs.reset()
	ups, err := wa.Main.Bridge.DB.UserPortal.GetAllForLogin(ctx, wa.UserLogin.UserLogin)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to get portals for presence subscriptions")
		return
	}
	slices.SortFunc(ups, func(a, b *database.UserPortal) int {
		return cmp.Compare(b.LastRead.UnixMilli(), a.LastRead.UnixMilli())
	})
	var jids []types.JID
	for _, up := range ups {
		if len(jids) >= wa.Main.Config.PresenceMaxSubscriptions {
			break
		}
		jid, err := waid.ParsePortalID(up.Portal.ID)
		if err != nil || !isPresenceSubscribable(jid) || wa.IsOwnJID(jid) {
			continue
		}
		if wa.presenceSubs.reserve(jid.ToNonAD(), wa.Main.Config.PresenceMaxSubscriptions) {
			jids = append(jids, jid.ToNonAD())
		}
	}
	zerolog.Ctx(ctx).Debug().Int("count", len(jids)).Msg("Subscribing to DM presences")
	for _, jid := range jids {
		if ctx.Err() != nil || !wa.IsLoggedIn() {
			return
		}
		if err := wa.Client.SubscribePresence(ctx, jid); err != nil {
			wa.presenceSubs.release(jid)
			zerolog.Ctx(ctx).Debug().Err(err).Stringer("jid", jid).Msg("Failed to subscribe to presence")
		}
		// Pace subscriptions to avoid looking like a scraper.
		time.Sleep(250 * time.Millisecond)
	}
}
