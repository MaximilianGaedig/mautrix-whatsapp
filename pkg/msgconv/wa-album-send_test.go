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
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func sendImage() *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}
}

func sendVideo() *waE2E.Message {
	return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{}}
}

func TestIsAlbumMedia(t *testing.T) {
	for name, tt := range map[string]struct {
		msg  *waE2E.Message
		want bool
	}{
		"image":      {sendImage(), true},
		"video":      {sendVideo(), true},
		"gif":        {&waE2E.Message{VideoMessage: &waE2E.VideoMessage{GifPlayback: proto.Bool(true)}}, false},
		"video note": {&waE2E.Message{PtvMessage: &waE2E.VideoMessage{}}, false},
		"view once":  {wrapViewOnce(sendImage()), false},
		"file":       {&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{}}, false},
		"audio":      {&waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}, false},
		"nothing":    {nil, false},
	} {
		if got := IsAlbumMedia(tt.msg); got != tt.want {
			t.Errorf("%s: expected %v, got %v", name, tt.want, got)
		}
	}
}

func TestNewAlbumParent(t *testing.T) {
	parent := NewAlbumParent([]*waE2E.Message{sendImage(), sendVideo(), sendImage()})
	if got := parent.GetAlbumMessage(); got.GetExpectedImageCount() != 2 || got.GetExpectedVideoCount() != 1 {
		t.Errorf("expected 2 images and 1 video, got %v", got)
	}
	// The counts are what the receiving side waits for, so only real album items count.
	parent = NewAlbumParent([]*waE2E.Message{sendImage(), nil, {DocumentMessage: &waE2E.DocumentMessage{}}, sendImage()})
	if got := parent.GetAlbumMessage(); got.GetExpectedImageCount() != 2 || got.ExpectedVideoCount != nil {
		t.Errorf("expected 2 images and no video count, got %v", got)
	}
	if got := albumExpectedCount(parent.GetAlbumMessage()); got != 2 {
		t.Errorf("the bridge's own reading of the parent should say 2 items, got %d", got)
	}
	for name, items := range map[string][]*waE2E.Message{
		"one image":             {sendImage()},
		"an image and a file":   {sendImage(), {DocumentMessage: &waE2E.DocumentMessage{}}},
		"an image and a failed": {sendImage(), nil},
		"nothing":               nil,
	} {
		if parent = NewAlbumParent(items); parent != nil {
			t.Errorf("%s is no album, got %v", name, parent)
		}
	}
}

func TestNewAlbumParentContext(t *testing.T) {
	first := sendImage()
	first.ImageMessage.ContextInfo = &waE2E.ContextInfo{
		StanzaID:       proto.String("QUOTED"),
		Participant:    proto.String("123@s.whatsapp.net"),
		QuotedMessage:  &waE2E.Message{Conversation: proto.String("")},
		Expiration:     proto.Uint32(86400),
		NonJIDMentions: proto.Uint32(1),
	}
	parent := NewAlbumParent([]*waE2E.Message{{DocumentMessage: &waE2E.DocumentMessage{}}, first, sendVideo()})
	ctxInfo := parent.GetAlbumMessage().GetContextInfo()
	if ctxInfo.GetStanzaID() != "QUOTED" || ctxInfo.GetParticipant() != "123@s.whatsapp.net" || ctxInfo.GetQuotedMessage() == nil {
		t.Errorf("the album should reply to what its first item replies to, got %v", ctxInfo)
	}
	if ctxInfo.GetExpiration() != 86400 {
		t.Errorf("the album should expire with its items, got %v", ctxInfo)
	}
	if ctxInfo.NonJIDMentions != nil {
		t.Errorf("only the reply and the timer are taken over, got %v", ctxInfo)
	}
	if parent = NewAlbumParent([]*waE2E.Message{sendImage(), sendImage()}); parent.GetAlbumMessage().ContextInfo != nil {
		t.Errorf("items without context give a parent without context")
	}
}

func TestLinkAlbumItems(t *testing.T) {
	key := &waCommon.MessageKey{RemoteJID: proto.String("123@s.whatsapp.net"), FromMe: proto.Bool(true), ID: proto.String("PARENT")}
	withSecret := sendVideo()
	withSecret.MessageContextInfo = &waE2E.MessageContextInfo{MessageSecret: []byte{1, 2, 3}}
	file := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{}}
	items := []*waE2E.Message{sendImage(), file, nil, withSecret}

	if linked := LinkAlbumItems(items, key); linked != 2 {
		t.Fatalf("expected 2 linked items, got %d", linked)
	}
	for wantIndex, item := range []*waE2E.Message{items[0], items[3]} {
		// The bridge reads incoming albums with GetAlbumParent, so what it sends must read back.
		if parent := GetAlbumParent(item); parent.GetID() != "PARENT" || !parent.GetFromMe() {
			t.Errorf("item %d doesn't point at the parent: %v", wantIndex, parent)
		}
		if got := item.GetMessageContextInfo().GetMessageAssociation().GetMessageIndex(); int(got) != wantIndex {
			t.Errorf("expected index %d, got %d", wantIndex, got)
		}
	}
	if file.MessageContextInfo != nil {
		t.Errorf("a file is not part of the album, got %v", file.MessageContextInfo)
	}
	if string(withSecret.MessageContextInfo.MessageSecret) != "\x01\x02\x03" {
		t.Errorf("linking must keep the rest of the message context")
	}
}
