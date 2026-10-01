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

package msgconv

import (
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

// albumMediaKind tells whether a message converted from Matrix can be an item of a WhatsApp
// album. Only plain images and videos can: a GIF plays in place, a video note and a view-once
// message are their own kind of message, and files and audio are never grouped.
func albumMediaKind(msg *waE2E.Message) (image, video bool) {
	switch {
	case msg == nil:
	case msg.ImageMessage != nil:
		image = true
	case msg.VideoMessage != nil:
		video = !msg.VideoMessage.GetGifPlayback()
	}
	return
}

// IsAlbumMedia reports whether the converted message can be an item of a WhatsApp album.
func IsAlbumMedia(msg *waE2E.Message) bool {
	image, video := albumMediaKind(msg)
	return image || video
}

func albumItemContextInfo(msg *waE2E.Message) *waE2E.ContextInfo {
	if msg.ImageMessage != nil {
		return msg.ImageMessage.GetContextInfo()
	}
	return msg.VideoMessage.GetContextInfo()
}

// NewAlbumParent makes the message that announces an album: WhatsApp expects it before the
// items, with the number of images and videos that will follow (see the top of wa-album.go).
// Entries that are nil or can't be in an album aren't counted. The result is nil if fewer than
// two items are left, as that is no album.
//
// The parent takes over the reply and the disappearing timer of the first item, so that it is
// a reply when the album is one, and doesn't outlive its items in a chat where messages expire.
func NewAlbumParent(items []*waE2E.Message) *waE2E.Message {
	var images, videos uint32
	var contextInfo *waE2E.ContextInfo
	for _, item := range items {
		image, video := albumMediaKind(item)
		if !image && !video {
			continue
		}
		if images+videos == 0 {
			if first := albumItemContextInfo(item); first != nil {
				contextInfo = &waE2E.ContextInfo{
					StanzaID:                  first.StanzaID,
					Participant:               first.Participant,
					QuotedMessage:             first.QuotedMessage,
					QuotedType:                first.QuotedType,
					Expiration:                first.Expiration,
					EphemeralSettingTimestamp: first.EphemeralSettingTimestamp,
				}
			}
		}
		if image {
			images++
		} else {
			videos++
		}
	}
	if images+videos < 2 {
		return nil
	}
	album := &waE2E.AlbumMessage{ContextInfo: contextInfo}
	// The counts are left unset rather than zero, like the official apps do.
	if images > 0 {
		album.ExpectedImageCount = proto.Uint32(images)
	}
	if videos > 0 {
		album.ExpectedVideoCount = proto.Uint32(videos)
	}
	return &waE2E.Message{AlbumMessage: album}
}

// SetAlbumParent marks a converted message as the item at the given position of the album that
// the parent key announces. It keeps whatever else the message context already holds.
func SetAlbumParent(msg *waE2E.Message, parent *waCommon.MessageKey, index int) {
	if msg.MessageContextInfo == nil {
		msg.MessageContextInfo = &waE2E.MessageContextInfo{}
	}
	msg.MessageContextInfo.MessageAssociation = &waE2E.MessageAssociation{
		AssociationType:  waE2E.MessageAssociation_MEDIA_ALBUM.Enum(),
		ParentMessageKey: parent,
		MessageIndex:     proto.Int32(int32(index)),
	}
}

// LinkAlbumItems marks every item that can be in an album as a child of the parent, numbered in
// their order. It returns how many items it marked.
func LinkAlbumItems(items []*waE2E.Message, parent *waCommon.MessageKey) (linked int) {
	for _, item := range items {
		if IsAlbumMedia(item) {
			SetAlbumParent(item, parent, linked)
			linked++
		}
	}
	return linked
}
