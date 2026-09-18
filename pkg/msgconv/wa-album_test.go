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
	"reflect"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/album"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func albumChild(parentID string) *waE2E.Message {
	return &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{},
		MessageContextInfo: &waE2E.MessageContextInfo{
			MessageAssociation: &waE2E.MessageAssociation{
				AssociationType:  waE2E.MessageAssociation_MEDIA_ALBUM.Enum(),
				ParentMessageKey: &waCommon.MessageKey{ID: proto.String(parentID)},
			},
		},
	}
}

func webMsg(id string, msg *waE2E.Message) *waWeb.WebMessageInfo {
	return &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{ID: proto.String(id)}, Message: msg}
}

func TestGetAlbumParent(t *testing.T) {
	if key := GetAlbumParent(albumChild("P")); key.GetID() != "P" {
		t.Errorf("expected parent P, got %v", key)
	}
	wrapped := &waE2E.Message{AssociatedChildMessage: &waE2E.FutureProofMessage{Message: albumChild("Q")}}
	if key := GetAlbumParent(nil, wrapped); key.GetID() != "Q" {
		t.Errorf("expected parent Q from associated child, got %v", key)
	}
	hd := albumChild("P")
	hd.MessageContextInfo.MessageAssociation.AssociationType = waE2E.MessageAssociation_HD_IMAGE_DUAL_UPLOAD.Enum()
	if key := GetAlbumParent(hd, &waE2E.Message{}); key != nil {
		t.Errorf("HD association must not be treated as album, got %v", key)
	}
}

func TestNewAlbumBatch(t *testing.T) {
	batch := NewAlbumBatch([]*waWeb.WebMessageInfo{
		webMsg("P", &waE2E.Message{AlbumMessage: &waE2E.AlbumMessage{ExpectedImageCount: proto.Uint32(2), ExpectedVideoCount: proto.Uint32(1)}}),
		webMsg("A", albumChild("P")),
		webMsg("X", &waE2E.Message{Conversation: proto.String("hi")}),
		webMsg("B", &waE2E.Message{EphemeralMessage: &waE2E.FutureProofMessage{Message: albumChild("P")}}),
		webMsg("C", albumChild("P")),
		webMsg("D", albumChild("R")),
		webMsg("A", albumChild("P")), // duplicate keeps its first index
	})
	expectedIndex := map[types.MessageID]int{"A": 0, "B": 1, "C": 2, "D": 0}
	if !reflect.DeepEqual(batch.Index, expectedIndex) {
		t.Errorf("unexpected indexes %v", batch.Index)
	}
	if !reflect.DeepEqual(batch.Count, map[types.MessageID]int{"P": 3}) {
		t.Errorf("unexpected counts %v", batch.Count)
	}
}

func TestNextAlbumIndex(t *testing.T) {
	recent := []*album.Info{{ID: "wa:P", Index: 1}, {ID: "wa:Q", Index: 5}, nil, {ID: "wa:P", Index: 0}}
	if idx := NextAlbumIndex("wa:P", recent); idx != 2 {
		t.Errorf("expected 2, got %d", idx)
	}
	if idx := NextAlbumIndex("wa:Z", recent); idx != 0 {
		t.Errorf("expected 0, got %d", idx)
	}
}

func imagePart() *bridgev2.ConvertedMessagePart {
	return &bridgev2.ConvertedMessagePart{Type: event.EventMessage, Content: &event.MessageEventContent{MsgType: event.MsgImage}}
}

func TestAddAlbumInfoFromBatch(t *testing.T) {
	var mc MessageConverter
	ctx := WithAlbumBatch(context.Background(), &AlbumBatch{
		Index: map[types.MessageID]int{"B": 1},
		Count: map[types.MessageID]int{"P": 3},
	})
	part := imagePart()
	meta := &waid.MessageMetadata{}
	msg := albumChild("P")
	mc.addAlbumInfo(ctx, nil, nil, &types.MessageInfo{ID: "B"}, msg, msg, part, meta)
	expected := &album.Info{ID: "wa:P", Index: 1, Count: 3}
	if got := album.Get(part.Extra); !reflect.DeepEqual(got, expected) {
		t.Errorf("expected %+v, got %+v", expected, got)
	}
	if !reflect.DeepEqual(meta.Album, expected) {
		t.Errorf("album info not stored in metadata: %+v", meta.Album)
	}
}

func TestAddAlbumInfoParentAndNonAlbum(t *testing.T) {
	var mc MessageConverter
	meta := &waid.MessageMetadata{}
	parentMsg := &waE2E.Message{AlbumMessage: &waE2E.AlbumMessage{ExpectedImageCount: proto.Uint32(4)}}
	notice := &bridgev2.ConvertedMessagePart{Type: event.EventMessage, Content: &event.MessageEventContent{MsgType: event.MsgNotice}}
	mc.addAlbumInfo(context.Background(), nil, nil, &types.MessageInfo{ID: "P"}, parentMsg, parentMsg, notice, meta)
	if meta.AlbumExpectedCount != 4 || notice.Extra != nil {
		t.Errorf("parent should only store the expected count: %+v %v", meta, notice.Extra)
	}

	part := imagePart()
	plain := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}
	mc.addAlbumInfo(context.Background(), nil, nil, &types.MessageInfo{ID: "S"}, plain, plain, part, &waid.MessageMetadata{})
	if part.Extra != nil {
		t.Errorf("single image must not be tagged: %v", part.Extra)
	}

	// Batch says the album only has one item: no field.
	one := imagePart()
	ctx := WithAlbumBatch(context.Background(), &AlbumBatch{
		Index: map[types.MessageID]int{"O": 0},
		Count: map[types.MessageID]int{"P1": 1},
	})
	child := albumChild("P1")
	mc.addAlbumInfo(ctx, nil, nil, &types.MessageInfo{ID: "O"}, child, child, one, &waid.MessageMetadata{})
	if one.Extra != nil {
		t.Errorf("single-item album must not be tagged: %v", one.Extra)
	}
}
