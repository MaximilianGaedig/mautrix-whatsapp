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
	"fmt"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// ViewOnce is the com.beeper.view_limited value of a Matrix media event that may be opened once. WhatsApp
// has no other kind of view limit, so it is the only one the room features list and the only one accepted
// here.
var ViewOnce = event.BeeperViewLimitedMedia{Type: "count", Count: 1}

// checkViewOnce refuses a view-once event that WhatsApp has no view-once form for. WhatsApp offers view-once
// for photos, videos and voice messages only. It runs before the media is uploaded, so that nothing is left
// on WhatsApp's servers for a message that is never sent.
func (mc *MessageConverter) checkViewOnce(content *event.MessageEventContent) error {
	limit := content.BeeperViewLimited
	if limit == nil {
		return nil
	}
	if mc.DisableViewOnce {
		return fmt.Errorf("%w: view-once messages are disabled on this bridge", bridgev2.ErrUnsupportedViewLimitedType)
	} else if *limit != ViewOnce {
		return fmt.Errorf("%w: WhatsApp media can only be limited to one view", bridgev2.ErrUnsupportedViewLimitedType)
	}
	switch content.GetCapMsgType() {
	case event.MsgImage, event.MsgVideo, event.CapMsgVoice:
		return nil
	default:
		return fmt.Errorf("%w: only photos, videos and voice messages can be view-once on WhatsApp", bridgev2.ErrUnsupportedViewLimitedType)
	}
}

// wrapViewOnce turns a media message into a view-once one, in the form the official apps send: the flag on
// the media message itself, inside ViewOnceMessageV2 for photos and videos and ViewOnceMessageV2Extension for
// voice messages. The context info (reply, disappearing timer) stays on the media message.
func wrapViewOnce(msg *waE2E.Message) *waE2E.Message {
	switch {
	case msg.GetImageMessage() != nil:
		msg.ImageMessage.ViewOnce = proto.Bool(true)
		return &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: msg}}
	case msg.GetVideoMessage() != nil:
		msg.VideoMessage.ViewOnce = proto.Bool(true)
		return &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{Message: msg}}
	case msg.GetAudioMessage() != nil:
		msg.AudioMessage.ViewOnce = proto.Bool(true)
		return &waE2E.Message{ViewOnceMessageV2Extension: &waE2E.FutureProofMessage{Message: msg}}
	default:
		return msg
	}
}
