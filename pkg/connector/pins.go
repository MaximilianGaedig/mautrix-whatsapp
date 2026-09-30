package connector

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"

	"go.mau.fi/mautrix-whatsapp/pkg/msgconv"
	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// WhatsApp pins always expire; Matrix pins don't, so pins from Matrix use the longest option WhatsApp offers.
const pinDuration = 30 * 24 * time.Hour

var _ bridgev2.PinHandlingNetworkAPI = (*WhatsAppClient)(nil)

// pinChange turns a pin message into the pinned-state change it stands for, or returns nil for pin
// types that don't change what is pinned.
func pinChange(pin *waE2E.PinInChatMessage, target networkid.MessageID) *bridgev2.ChatInfo {
	if target == "" {
		return nil
	}
	var pinned bool
	switch pin.GetType() {
	case waE2E.PinInChatMessage_PIN_FOR_ALL:
		pinned = true
	case waE2E.PinInChatMessage_UNPIN_FOR_ALL:
		pinned = false
	default:
		return nil
	}
	return &bridgev2.ChatInfo{
		PinChanges: []bridgev2.PinChange{{MessageID: target, Pinned: pinned}},
	}
}

func (wa *WhatsAppClient) handleWAPinInChat(ctx context.Context, evt *MessageInfoWrapper, pin *waE2E.PinInChatMessage) bool {
	target := msgconv.KeyToMessageID(ctx, wa.Client, evt.Info.Chat, evt.Info.Sender, pin.GetKey())
	info := pinChange(pin, target)
	if info == nil {
		wa.UserLogin.Log.Debug().
			Str("message_id", evt.Info.ID).
			Stringer("pin_type", pin.GetType()).
			Msg("Ignoring pin message without a known target or type")
		return true
	}
	return wa.UserLogin.QueueRemoteEvent(&simplevent.ChatInfoChange{
		EventMeta: simplevent.EventMeta{
			Type: bridgev2.RemoteEventChatInfoChange,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("wa_event_type", "pin_in_chat").Str("target_message_id", string(target))
			},
			PortalKey: evt.GetPortalKey(),
			Sender:    evt.GetSender(),
			Timestamp: evt.Info.Timestamp,
		},
		ChatInfoChange: &bridgev2.ChatInfoChange{ChatInfo: info},
	}).Success
}

// pinInChatMessage builds the message that pins or unpins key for everyone in the chat.
func pinInChatMessage(key *waCommon.MessageKey, pinned bool, ts time.Time) *waE2E.Message {
	pinType := waE2E.PinInChatMessage_UNPIN_FOR_ALL
	msg := &waE2E.Message{
		PinInChatMessage: &waE2E.PinInChatMessage{
			Key:               key,
			SenderTimestampMS: ptr.Ptr(ts.UnixMilli()),
		},
	}
	if pinned {
		pinType = waE2E.PinInChatMessage_PIN_FOR_ALL
		msg.MessageContextInfo = &waE2E.MessageContextInfo{
			MessageAddOnDurationInSecs: ptr.Ptr(uint32(pinDuration / time.Second)),
			MessageAddOnExpiryType:     waE2E.MessageContextInfo_STATIC.Enum(),
		}
	}
	msg.PinInChatMessage.Type = pinType.Enum()
	return msg
}

func (wa *WhatsAppClient) HandleMatrixPin(ctx context.Context, msg *bridgev2.MatrixPin) error {
	messageID, err := waid.ParseMessageID(msg.TargetMessage.ID)
	if err != nil {
		return fmt.Errorf("failed to parse target message ID: %w", err)
	}
	portalJID, err := waid.ParsePortalID(msg.Portal.ID)
	if err != nil {
		return fmt.Errorf("failed to parse portal ID: %w", err)
	}
	_, err = wa.Client.SendMessage(ctx, portalJID, pinInChatMessage(wa.messageIDToKey(messageID), msg.Pinned, time.UnixMilli(msg.Event.Timestamp)))
	return err
}
