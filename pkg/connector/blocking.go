package connector

import (
	"context"
	"fmt"
	"sync"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

var _ bridgev2.UserBlockingNetworkAPI = (*WhatsAppClient)(nil)

// blockUpdate is one ghost whose blocked state changes on Matrix.
type blockUpdate struct {
	GhostID networkid.UserID
	Blocked bool
	// Alt is set for the ghost of the other form (LID or phone number) of a user. It may not exist, and
	// then it is not blocked, since nothing on Matrix could be talking to it.
	Alt bool
}

// blockedGhosts tracks which ghosts this login has marked blocked, so that a full list can unblock the ones it
// no longer holds.
type blockedGhosts struct {
	lock sync.Mutex
	ids  map[networkid.UserID]struct{}
}

func (b *blockedGhosts) set(id networkid.UserID, blocked bool) {
	b.lock.Lock()
	defer b.lock.Unlock()
	if b.ids == nil {
		b.ids = make(map[networkid.UserID]struct{})
	}
	if blocked {
		b.ids[id] = struct{}{}
	} else {
		delete(b.ids, id)
	}
}

func (b *blockedGhosts) snapshot() map[networkid.UserID]struct{} {
	b.lock.Lock()
	defer b.lock.Unlock()
	out := make(map[networkid.UserID]struct{}, len(b.ids))
	for id := range b.ids {
		out[id] = struct{}{}
	}
	return out
}

// blockGhostUpdates maps a user to the ghosts that represent them: the one for the JID itself, and the one for
// the other form of it (LID or phone number) when that is known.
func blockGhostUpdates(jid, alt types.JID, blocked bool) []blockUpdate {
	id := waid.MakeUserID(jid)
	if id == "" {
		return nil
	}
	updates := []blockUpdate{{GhostID: id, Blocked: blocked}}
	if altID := waid.MakeUserID(alt); altID != "" && altID != id {
		updates = append(updates, blockUpdate{GhostID: altID, Blocked: blocked, Alt: true})
	}
	return updates
}

// blocklistUpdates maps a blocklist change event to ghost updates. A "modify" event carries no changes: the
// whole list has to be fetched, which is what full reports.
func blocklistUpdates(evt *events.Blocklist, altJID func(types.JID) types.JID) (updates []blockUpdate, full bool) {
	if evt.Action == events.BlocklistActionModify {
		return nil, true
	}
	for _, change := range evt.Changes {
		var blocked bool
		switch change.Action {
		case events.BlocklistChangeActionBlock:
			blocked = true
		case events.BlocklistChangeActionUnblock:
		default:
			continue
		}
		updates = append(updates, blockGhostUpdates(change.JID, altJID(change.JID), blocked)...)
	}
	return
}

// blocklistSnapshotUpdates maps the whole blocklist to ghost updates: block what it holds, and unblock what was
// blocked before and is gone from it.
func blocklistSnapshotUpdates(prev map[networkid.UserID]struct{}, list []types.JID, altJID func(types.JID) types.JID) []blockUpdate {
	var updates []blockUpdate
	now := make(map[networkid.UserID]struct{})
	for _, jid := range list {
		for _, update := range blockGhostUpdates(jid, altJID(jid), true) {
			updates = append(updates, update)
			now[update.GhostID] = struct{}{}
		}
	}
	for id := range prev {
		if _, still := now[id]; !still {
			updates = append(updates, blockUpdate{GhostID: id, Blocked: false})
		}
	}
	return updates
}

func (wa *WhatsAppClient) altJIDForBlock(ctx context.Context) func(types.JID) types.JID {
	return func(jid types.JID) types.JID {
		if jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer {
			return types.EmptyJID
		}
		alt, err := wa.GetStore().GetAltJID(ctx, jid)
		if err != nil {
			return types.EmptyJID
		}
		return alt
	}
}

func (wa *WhatsAppClient) applyBlockUpdates(ctx context.Context, updates []blockUpdate) {
	log := wa.UserLogin.Log
	for _, update := range updates {
		if update.Alt && update.Blocked {
			ghost, err := wa.Main.Bridge.GetExistingGhostByID(ctx, update.GhostID)
			if err != nil || ghost == nil {
				continue
			}
		}
		if err := wa.UserLogin.SetGhostBlocked(ctx, update.GhostID, update.Blocked); err != nil {
			log.Err(err).Str("ghost_id", string(update.GhostID)).Bool("blocked", update.Blocked).Msg("Failed to update blocked ghost on Matrix")
			continue
		}
		wa.blocked.set(update.GhostID, update.Blocked)
	}
}

func (wa *WhatsAppClient) handleWABlocklist(ctx context.Context, evt *events.Blocklist) {
	updates, full := blocklistUpdates(evt, wa.altJIDForBlock(ctx))
	if full {
		wa.syncBlocklist(ctx)
		return
	}
	wa.applyBlockUpdates(ctx, updates)
}

// syncBlocklist fetches the whole blocklist and makes the ghosts match it.
func (wa *WhatsAppClient) syncBlocklist(ctx context.Context) {
	list, err := wa.Client.GetBlocklist(ctx)
	if err != nil {
		wa.UserLogin.Log.Warn().Err(err).Msg("Failed to get blocklist")
		return
	}
	wa.applyBlockUpdates(ctx, blocklistSnapshotUpdates(wa.blocked.snapshot(), list.JIDs, wa.altJIDForBlock(ctx)))
}

func (wa *WhatsAppClient) HandleMatrixBlock(ctx context.Context, ghost *bridgev2.Ghost, blocked bool) error {
	jid := waid.ParseUserID(ghost.ID)
	if jid.Server != types.DefaultUserServer && jid.Server != types.HiddenUserServer {
		return fmt.Errorf("can't block %s: not a user", jid)
	}
	action := events.BlocklistChangeActionUnblock
	if blocked {
		action = events.BlocklistChangeActionBlock
	}
	if _, err := wa.Client.UpdateBlocklist(ctx, jid, action); err != nil {
		return fmt.Errorf("update blocklist: %w", err)
	}
	wa.blocked.set(ghost.ID, blocked)
	return nil
}
