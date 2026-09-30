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

func (wa *WhatsAppClient) handleWAPinInChat(ctx context.Context, evt *WAMessageEvent, pin *waE2E.PinInChatMessage) bool {
	target := msgconv.KeyToMessageID(ctx, wa.Client, evt.Info.Chat, evt.Info.Sender, pin.GetKey())
	info := pinChange(pin, target)
	if info != nil {
		pinned := info.PinChanges[0].Pinned
		expiry := pinExpiry(evt.Info.Timestamp, evt.Message.GetMessageContextInfo().GetMessageAddOnDurationInSecs())
		info.ExtraUpdates = func(ctx context.Context, portal *bridgev2.Portal) bool {
			return portal.Metadata.(*waid.PortalMetadata).SetPinExpiry(target, pinned, expiry)
		}
		if pinned {
			wa.schedulePinExpiry(evt.GetPortalKey(), target, expiry)
		}
	}
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
	ts := time.UnixMilli(msg.Event.Timestamp)
	_, err = wa.Client.SendMessage(ctx, portalJID, pinInChatMessage(wa.messageIDToKey(messageID), msg.Pinned, ts))
	if err != nil {
		return err
	}
	var expiry time.Time
	if msg.Pinned {
		expiry = ts.Add(pinDuration)
		wa.schedulePinExpiry(msg.Portal.PortalKey, msg.TargetMessage.ID, expiry)
	}
	if msg.Portal.Metadata.(*waid.PortalMetadata).SetPinExpiry(msg.TargetMessage.ID, msg.Pinned, expiry) {
		return msg.Portal.Save(ctx)
	}
	return nil
}

// afterFunc is time.AfterFunc; tests count the timers through it.
var afterFunc = func(d time.Duration, f func()) { time.AfterFunc(d, f) }

type pinTimerKey struct {
	portal networkid.PortalKey
	msgID  networkid.MessageID
	expiry int64
}

// pinExpiry is when a pin made at ts for duration seconds runs out; zero for a pin without a duration.
func pinExpiry(ts time.Time, duration uint32) time.Time {
	if duration == 0 {
		return time.Time{}
	}
	return ts.Add(time.Duration(duration) * time.Second)
}

// schedulePinExpiry unpins the message in Matrix when its WhatsApp pin runs out, as WhatsApp does
// silently on every device. A pin that ran out while the bridge was down is unpinned at the next
// connect (sweepPinExpiry).
func (wa *WhatsAppClient) schedulePinExpiry(portalKey networkid.PortalKey, msgID networkid.MessageID, expiry time.Time) {
	if expiry.IsZero() {
		return
	}
	// Every reconnect re-arms the timers; one per pin is enough.
	key := pinTimerKey{portalKey, msgID, expiry.Unix()}
	if _, armed := wa.pinTimers.LoadOrStore(key, struct{}{}); armed {
		return
	}
	afterFunc(max(time.Until(expiry), 0), func() {
		wa.pinTimers.Delete(key)
		wa.expirePin(portalKey, msgID, expiry)
	})
}

func (wa *WhatsAppClient) expirePin(portalKey networkid.PortalKey, msgID networkid.MessageID, expiry time.Time) {
	ctx := wa.UserLogin.Log.With().Str("action", "expire pin").Logger().WithContext(wa.Main.Bridge.BackgroundCtx)
	portal, err := wa.Main.Bridge.GetExistingPortalByKey(ctx, portalKey)
	if err != nil || portal == nil {
		return
	}
	// Re-pinned since (a later expiry) or unpinned already: this timer is stale.
	if at, ok := portal.Metadata.(*waid.PortalMetadata).PinExpiry[msgID]; !ok || at.Unix() != expiry.Unix() {
		return
	}
	wa.UserLogin.QueueRemoteEvent(&simplevent.ChatInfoChange{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatInfoChange,
			PortalKey: portalKey,
			Timestamp: expiry,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str("wa_event_type", "pin_expired").Str("target_message_id", string(msgID))
			},
		},
		ChatInfoChange: &bridgev2.ChatInfoChange{ChatInfo: &bridgev2.ChatInfo{
			PinChanges: []bridgev2.PinChange{{MessageID: msgID, Pinned: false}},
			ExtraUpdates: func(ctx context.Context, portal *bridgev2.Portal) bool {
				return portal.Metadata.(*waid.PortalMetadata).SetPinExpiry(msgID, false, time.Time{})
			},
		}},
	})
}

// sweepPinExpiry re-arms the pin timers of this login's portals after a (re)start.
func (wa *WhatsAppClient) sweepPinExpiry(ctx context.Context) {
	portals, err := wa.Main.Bridge.GetAllPortalsWithMXID(ctx)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to list portals for pin expiry")
		return
	}
	for _, portal := range portals {
		if portal.Receiver != "" && portal.Receiver != wa.UserLogin.ID {
			continue
		}
		for msgID, at := range portal.Metadata.(*waid.PortalMetadata).PinExpiry {
			wa.schedulePinExpiry(portal.PortalKey, msgID, at.Time)
		}
	}
}
