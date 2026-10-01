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

package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

var _ bridgev2.AlbumHandlingNetworkAPI = (*WhatsAppClient)(nil)

// HandleMatrixAlbum sends media messages that belong together on Matrix as one WhatsApp album.
//
// A WhatsApp album is an announcement followed by ordinary media messages that point back at it
// (see pkg/msgconv/wa-album.go). So every Matrix event is still sent as its own message, through
// the same code as a message sent alone, and only the announcement comes on top. Files and audio
// can't be in an album: they are sent in their place, without the pointer.
func (wa *WhatsAppClient) HandleMatrixAlbum(ctx context.Context, msgs []*bridgev2.MatrixMessage) ([]bridgev2.MatrixAlbumPartResult, error) {
	log := zerolog.Ctx(ctx)
	results := make([]bridgev2.MatrixAlbumPartResult, len(msgs))
	converted := make([]*waE2E.Message, len(msgs))
	reqs := make([]*whatsmeow.SendRequestExtra, len(msgs))
	for i, msg := range msgs {
		var err error
		converted[i], reqs[i], err = wa.Main.MsgConv.ToWhatsApp(ctx, wa.Client, msg.Event, msg.Content, msg.ReplyTo, msg.ThreadRoot, msg.Portal)
		if err != nil {
			// The item stays nil, which keeps it out of the album's count.
			results[i].Err = fmt.Errorf("failed to convert message: %w", err)
		}
	}

	if parent := msgconv.NewAlbumParent(converted); parent != nil {
		parentKey, forgetParent, err := wa.sendAlbumParent(ctx, msgs[0], parent)
		if err != nil {
			// The media is worth more than its grouping: without the announcement the items
			// are sent as separate messages.
			log.Err(err).Msg("Failed to send album parent, sending items without an album")
		} else {
			defer forgetParent()
			linked := msgconv.LinkAlbumItems(converted, parentKey)
			log.Debug().Str("album_parent_id", parentKey.GetID()).Int("items", linked).Msg("Sent album parent")
		}
	}

	for i, msg := range msgs {
		if results[i].Err != nil {
			continue
		}
		start := time.Now()
		results[i].Response, results[i].Err = wa.handleConvertedMatrixMessage(ctx, msg, converted[i], reqs[i])
		wa.mcTrack(msg, start, &results[i].Err)
	}
	return results, nil
}

// sendAlbumParent sends the announcement of an album and returns the key its items point at.
// The returned function must be called once the items are sent.
func (wa *WhatsAppClient) sendAlbumParent(ctx context.Context, msg *bridgev2.MatrixMessage, parent *waE2E.Message) (*waCommon.MessageKey, func(), error) {
	chatJID, err := waid.ParsePortalID(msg.Portal.ID)
	if err != nil {
		return nil, nil, err
	}
	if chatJID == types.StatusBroadcastJID {
		return nil, nil, fmt.Errorf("status messages have no albums")
	}
	parentID := wa.Client.GenerateMessageID()
	// The announcement has no Matrix event. If it came back as an incoming message, it would be
	// bridged as a "Sent an album" notice, so it is ignored for as long as the album is sent.
	pending := []networkid.TransactionID{
		networkid.TransactionID(waid.MakeMessageID(chatJID, wa.JID, parentID)),
		networkid.TransactionID(waid.MakeMessageID(chatJID, wa.GetLID(), parentID)),
	}
	for _, txnID := range pending {
		msg.AddPendingToIgnore(txnID)
	}
	forget := func() {
		for _, txnID := range pending {
			msg.RemovePending(txnID)
		}
	}
	_, err = wa.Client.SendMessage(ctx, chatJID, parent, whatsmeow.SendRequestExtra{ID: parentID})
	if err != nil {
		forget()
		return nil, nil, err
	}
	return &waCommon.MessageKey{
		RemoteJID: ptr.Ptr(chatJID.String()),
		FromMe:    ptr.Ptr(true),
		ID:        ptr.Ptr(parentID),
	}, forget, nil
}
