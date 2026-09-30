package connector

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

var _ bridgev2.ChatListSyncingNetworkAPI = (*WhatsAppClient)(nil)

// SyncChatList goes over every chat again - the groups the account is in, as WhatsApp lists them now, and
// every direct chat the history sync told us about, whether or not it already has a room - and queues a
// resync for each that creates a room where there is none (e.g. after delete-portal).
func (wa *WhatsAppClient) SyncChatList(ctx context.Context) error {
	if wa.Client == nil || !wa.Client.IsLoggedIn() {
		return errors.New("not connected to WhatsApp")
	}
	log := zerolog.Ctx(ctx)
	groups, err := wa.Client.GetJoinedGroups(ctx)
	if err != nil {
		return err
	}
	for _, group := range groups {
		wa.UserLogin.QueueRemoteEvent(&simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatResync,
				PortalKey:    wa.makeWAPortalKey(group.JID),
				CreatePortal: true,
			},
			GetChatInfoFunc: func(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
				wrapped := wa.wrapGroupInfo(ctx, group)
				wrapped.ExtraUpdates = bridgev2.MergeExtraUpdaters(wrapped.ExtraUpdates, updatePortalLastSyncAt)
				wa.addExtrasToWrapped(ctx, group.JID, wrapped, nil, portal.MXID == "")
				return wrapped, nil
			},
		})
	}
	// Every stored conversation: a cutoff in the future includes the ones already synced.
	conversations, err := wa.Main.DB.Conversation.GetRecent(ctx, wa.UserLogin.ID, -1, jsontime.U(time.Now().Add(time.Hour)))
	if err != nil {
		return err
	}
	direct := 0
	for _, conv := range conversations {
		if conv.ChatJID.Server == types.GroupServer ||
			conv.ChatJID == types.StatusBroadcastJID ||
			conv.ChatJID == types.PSAJID || conv.ChatJID == types.LegacyPSAJID {
			continue
		}
		direct++
		wa.UserLogin.QueueRemoteEvent(&simplevent.ChatResync{
			EventMeta: simplevent.EventMeta{
				Type:         bridgev2.RemoteEventChatResync,
				PortalKey:    wa.makeWAPortalKey(conv.ChatJID),
				CreatePortal: true,
			},
			GetChatInfoFunc: wa.GetChatInfo,
			LatestMessageTS: conv.LastMessageTimestamp,
		})
	}
	log.Info().Int("groups", len(groups)).Int("direct_chats", direct).Msg("Queued chat list resync")
	return nil
}
