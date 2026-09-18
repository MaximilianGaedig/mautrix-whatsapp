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

package msgconv

import (
	"context"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"

	"go.mau.fi/mautrix-whatsapp/pkg/album"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// How WhatsApp albums work: the sender first sends an AlbumMessage (the
// "parent", bridged as a "Sent an album with N images" notice) which contains
// the expected image and video counts. Every item is then sent as a separate
// normal image/video message (the "children"), whose
// messageContextInfo.messageAssociation has type MEDIA_ALBUM and points at the
// parent message key. Children don't carry their position, so the index is
// their arrival order.

// albumRecentMessageScan is how many recent portal messages are checked for
// earlier items of the same album when bridging a new child.
const albumRecentMessageScan = 50

func albumID(parentID types.MessageID) string {
	return "wa:" + parentID
}

func albumExpectedCount(msg *waE2E.AlbumMessage) int {
	return int(msg.GetExpectedImageCount() + msg.GetExpectedVideoCount())
}

// GetAlbumParent returns the parent message key if the message is an item of an album.
func GetAlbumParent(msgs ...*waE2E.Message) *waCommon.MessageKey {
	for _, msg := range msgs {
		for _, candidate := range []*waE2E.Message{msg, msg.GetAssociatedChildMessage().GetMessage()} {
			assoc := candidate.GetMessageContextInfo().GetMessageAssociation()
			if assoc.GetAssociationType() == waE2E.MessageAssociation_MEDIA_ALBUM && assoc.GetParentMessageKey().GetID() != "" {
				return assoc.GetParentMessageKey()
			}
		}
	}
	return nil
}

// AlbumBatch contains precomputed album indexes for a batch of messages which
// are converted together (history sync backfill). Those messages aren't in the
// database yet, so the database scan used for live messages can't see them.
type AlbumBatch struct {
	// Index maps child message IDs to their index within their album.
	Index map[types.MessageID]int
	// Count maps album parent message IDs to their expected item count.
	Count map[types.MessageID]int
}

type albumBatchContextKey struct{}

// NewAlbumBatch computes album indexes for messages in chronological order.
func NewAlbumBatch(chronological []*waWeb.WebMessageInfo) *AlbumBatch {
	batch := &AlbumBatch{Index: make(map[types.MessageID]int), Count: make(map[types.MessageID]int)}
	next := make(map[types.MessageID]int)
	for _, info := range chronological {
		msg := info.GetMessage()
		if unwrapped := unwrapForAlbum(msg); unwrapped != nil {
			if albumMsg := unwrapped.GetAlbumMessage(); albumMsg != nil {
				batch.Count[info.GetKey().GetID()] = albumExpectedCount(albumMsg)
				continue
			}
			msg = unwrapped
		}
		parent := GetAlbumParent(msg, info.GetMessage())
		if parent == nil {
			continue
		}
		childID := info.GetKey().GetID()
		if _, seen := batch.Index[childID]; seen {
			continue
		}
		batch.Index[childID] = next[parent.GetID()]
		next[parent.GetID()]++
	}
	return batch
}

// unwrapForAlbum strips the wrappers that can be around album messages.
func unwrapForAlbum(msg *waE2E.Message) *waE2E.Message {
	for range 5 {
		switch {
		case msg.GetDeviceSentMessage().GetMessage() != nil:
			msg = msg.GetDeviceSentMessage().GetMessage()
		case msg.GetEphemeralMessage().GetMessage() != nil:
			msg = msg.GetEphemeralMessage().GetMessage()
		case msg.GetViewOnceMessage().GetMessage() != nil:
			msg = msg.GetViewOnceMessage().GetMessage()
		case msg.GetViewOnceMessageV2().GetMessage() != nil:
			msg = msg.GetViewOnceMessageV2().GetMessage()
		default:
			return msg
		}
	}
	return msg
}

// WithAlbumBatch stores precomputed album indexes in the context for ToMatrix.
func WithAlbumBatch(ctx context.Context, batch *AlbumBatch) context.Context {
	return context.WithValue(ctx, albumBatchContextKey{}, batch)
}

func getAlbumBatch(ctx context.Context) *AlbumBatch {
	batch, _ := ctx.Value(albumBatchContextKey{}).(*AlbumBatch)
	return batch
}

// NextAlbumIndex returns the index for a new album item given the album info
// of recent messages in the portal.
func NextAlbumIndex(id string, recent []*album.Info) int {
	next := 0
	for _, info := range recent {
		if info != nil && info.ID == id && info.Index >= next {
			next = info.Index + 1
		}
	}
	return next
}

func (mc *MessageConverter) addAlbumInfo(
	ctx context.Context,
	portal *bridgev2.Portal,
	client *whatsmeow.Client,
	info *types.MessageInfo,
	waMsg, rawWaMsg *waE2E.Message,
	part *bridgev2.ConvertedMessagePart,
	dbMeta *waid.MessageMetadata,
) {
	if albumMsg := waMsg.GetAlbumMessage(); albumMsg != nil {
		dbMeta.AlbumExpectedCount = albumExpectedCount(albumMsg)
		return
	}
	// Edits normally don't carry the association, ConvertEdit copies the
	// album info of the original message instead.
	if !album.IsMediaPart(part) {
		return
	}
	parentKey := GetAlbumParent(waMsg, rawWaMsg)
	if parentKey == nil {
		return
	}
	log := zerolog.Ctx(ctx)
	albumInfo := album.Info{ID: albumID(parentKey.GetID())}
	batch := getAlbumBatch(ctx)
	if idx, ok := batch.index(info.ID); ok {
		albumInfo.Index = idx
		albumInfo.Count = batch.Count[parentKey.GetID()]
	} else {
		albumInfo.Index = mc.albumIndexFromDB(ctx, portal, albumInfo.ID, info)
	}
	if albumInfo.Count == 0 && portal != nil {
		parentID := KeyToMessageID(ctx, client, info.Chat, info.Sender, parentKey)
		if parentID != "" {
			parent, err := portal.Bridge.DB.Message.GetFirstPartByID(ctx, portal.Receiver, parentID)
			if err != nil {
				log.Warn().Err(err).Msg("Failed to get album parent message")
			} else if parent != nil {
				if meta, ok := parent.Metadata.(*waid.MessageMetadata); ok {
					albumInfo.Count = meta.AlbumExpectedCount
				}
			}
		}
	}
	if albumInfo.Count == 1 {
		// Single-item albums don't get the field.
		return
	}
	album.Set(part, albumInfo)
	dbMeta.Album = &albumInfo
}

func (batch *AlbumBatch) index(id types.MessageID) (int, bool) {
	if batch == nil {
		return 0, false
	}
	idx, ok := batch.Index[id]
	return idx, ok
}

func (mc *MessageConverter) albumIndexFromDB(ctx context.Context, portal *bridgev2.Portal, id string, info *types.MessageInfo) int {
	if portal == nil {
		return 0
	}
	recent, err := portal.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, albumRecentMessageScan)
	if err != nil {
		zerolog.Ctx(ctx).Warn().Err(err).Msg("Failed to get recent messages for album index")
		return 0
	}
	ownID := waid.MakeMessageIDWithAltSender(info.Chat, info.Sender, info.SenderAlt, info.ID)
	infos := make([]*album.Info, 0, len(recent))
	for _, msg := range recent {
		meta, ok := msg.Metadata.(*waid.MessageMetadata)
		if !ok || meta.Album == nil {
			continue
		}
		if msg.ID == ownID {
			// Already bridged (e.g. retried decryption), keep the old index.
			return meta.Album.Index
		}
		infos = append(infos, meta.Album)
	}
	return NextAlbumIndex(id, infos)
}
