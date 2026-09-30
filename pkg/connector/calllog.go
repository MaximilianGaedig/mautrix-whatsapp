package connector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/calllog"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const callEventMaxAge = 15 * time.Minute

// callLogReport maps a whatsmeow call event onto the call log and returns the remote event to queue, or nil
// when the event changes nothing on screen. mkCall resolves the portal and sender of an offer; it returns false
// when the offer should not be logged.
func callLogReport(l *calllog.Log, rawEvt any, mkCall func(meta *types.BasicCallMeta) (calllog.Call, bool)) bridgev2.RemoteEvent {
	switch evt := rawEvt.(type) {
	case *events.CallOffer:
		call, ok := mkCall(&evt.BasicCallMeta)
		if !ok {
			return nil
		}
		call.Video = evt.Video
		call.Group = !evt.GroupJID.IsEmpty()
		return l.Start(evt.CallID, call)
	case *events.CallOfferNotice:
		call, ok := mkCall(&evt.BasicCallMeta)
		if !ok {
			return nil
		}
		call.Video = evt.Media == "video" || evt.Type == "video"
		call.Group = !evt.GroupJID.IsEmpty()
		return l.Start(evt.CallID, call)
	case *events.CallAccept:
		return l.Answer(evt.CallID, evt.Timestamp)
	case *events.CallReject:
		return l.Decline(evt.CallID, evt.Timestamp)
	case *events.CallTerminate:
		return l.End(evt.CallID, evt.Timestamp)
	}
	return nil
}

// ringingCall is an incoming call that has not been answered, declined or ended.
type ringingCall struct {
	ID   string
	From types.JID
}

// ringingCalls keeps the last ringing call of each portal, for decline-call.
type ringingCalls struct {
	lock  sync.Mutex
	calls map[networkid.PortalKey]ringingCall
}

func (r *ringingCalls) set(portal networkid.PortalKey, call ringingCall) {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.calls == nil {
		r.calls = make(map[networkid.PortalKey]ringingCall)
	}
	r.calls[portal] = call
}

func (r *ringingCalls) get(portal networkid.PortalKey) (ringingCall, bool) {
	r.lock.Lock()
	defer r.lock.Unlock()
	call, ok := r.calls[portal]
	return call, ok
}

// clear forgets the call with the given ID, wherever it rang.
func (r *ringingCalls) clear(callID string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	for key, call := range r.calls {
		if call.ID == callID {
			delete(r.calls, key)
		}
	}
}

func callEventID(rawEvt any) (id string, over bool) {
	switch evt := rawEvt.(type) {
	case *events.CallAccept:
		return evt.CallID, true
	case *events.CallReject:
		return evt.CallID, true
	case *events.CallTerminate:
		return evt.CallID, true
	}
	return "", false
}

// handleWACallEvent puts a call event in the call log. The log is keyed by call ID, so the offer, the offer
// notice and the call bridge's own reports of one call never post twice.
func (wa *WhatsAppClient) handleWACallEvent(ctx context.Context, rawEvt any) bool {
	if !wa.Main.Config.CallStartNotices {
		return true
	}
	var portal networkid.PortalKey
	var offered *ringingCall
	evt := callLogReport(wa.CallLog, rawEvt, func(meta *types.BasicCallMeta) (calllog.Call, bool) {
		if time.Since(meta.Timestamp) > callEventMaxAge || wa.IsOwnJID(meta.CallCreator) {
			return calllog.Call{}, false
		}
		call := wa.resolveCallStart(ctx, meta)
		portal = call.Portal
		offered = &ringingCall{ID: meta.CallID, From: meta.CallCreator}
		return call, true
	})
	if id, over := callEventID(rawEvt); over {
		wa.Ringing.clear(id)
	} else if evt != nil && offered != nil {
		wa.Ringing.set(portal, *offered)
	}
	if evt == nil {
		return true
	}
	return wa.UserLogin.QueueRemoteEvent(evt).Success
}

// resolveCallStart finds the chat and sender of a call offer. A caller known by phone number is forced to
// their LID, like other events from them.
func (wa *WhatsAppClient) resolveCallStart(ctx context.Context, meta *types.BasicCallMeta) calllog.Call {
	sender, senderAlt := meta.CallCreator, meta.CallCreatorAlt
	if sender.Server == types.DefaultUserServer && senderAlt.IsEmpty() {
		senderAlt, _ = wa.GetStore().LIDs.GetLIDForPN(ctx, sender)
	}
	if sender.Server == types.DefaultUserServer && senderAlt.Server == types.HiddenUserServer {
		wa.UserLogin.Log.Debug().
			Stringer("lid", senderAlt).
			Stringer("pn", sender).
			Str("call_id", meta.CallID).
			Msg("Forced phone number caller to LID in incoming call")
		sender, senderAlt = senderAlt, sender
	}
	chat := meta.GroupJID
	if chat.IsEmpty() {
		chat = sender
	}
	return calllog.Call{
		Portal:  wa.makeWAPortalKey(chat),
		Caller:  wa.makeEventSender(ctx, sender),
		Started: meta.Timestamp,
	}
}

// reportBridgedCall tells the call log about a call the call bridge answered or ended on its own, which
// WhatsApp does not echo back as an event.
func (wa *WhatsAppClient) reportBridgedCall(callID string, report func(*calllog.Log, string, time.Time) bridgev2.RemoteEvent) {
	if !wa.Main.Config.CallStartNotices || wa.CallLog == nil {
		return
	}
	wa.Ringing.clear(callID)
	if evt := report(wa.CallLog, callID, time.Now()); evt != nil {
		wa.UserLogin.QueueRemoteEvent(evt)
	}
}

var cmdDeclineCall = &commands.FullHandler{
	Func: fnDeclineCall,
	Name: "decline-call",
	Help: commands.HelpMeta{
		Section:     commands.HelpSectionMisc,
		Description: "Decline the WhatsApp call that is ringing in this chat.",
	},
	RequiresLogin:  true,
	RequiresPortal: true,
}

func fnDeclineCall(ce *commands.Event) {
	login := ce.Bridge.GetCachedUserLoginByID(ce.Portal.Receiver)
	if login == nil {
		login = ce.User.GetDefaultLogin()
	}
	client, ok := login.Client.(*WhatsAppClient)
	if !ok || client == nil || !client.IsLoggedIn() {
		ce.Reply("Not logged in")
		return
	}
	call, ok := client.Ringing.get(ce.Portal.PortalKey)
	if !ok {
		ce.Reply("No call is ringing in this chat.")
		return
	}
	if err := client.declineCall(ce.Ctx, call); err != nil {
		ce.Log.Err(err).Str("call_id", call.ID).Msg("Failed to decline call")
		ce.Reply("Failed to decline the call: %v", err)
		return
	}
	ce.Reply("Declined the call.")
}

func (wa *WhatsAppClient) declineCall(ctx context.Context, call ringingCall) error {
	// A bridged call is rejected through its session, so the two sides of it agree.
	if calls := wa.Calls; calls != nil {
		calls.lock.Lock()
		session := calls.active
		calls.lock.Unlock()
		if session != nil && session.incoming && session.call != nil && session.call.ID() == call.ID {
			session.end(true)
			wa.reportBridgedCall(call.ID, (*calllog.Log).Decline)
			return nil
		}
	}
	if err := wa.Client.RejectCall(ctx, call.From, call.ID); err != nil {
		return fmt.Errorf("reject call: %w", err)
	}
	wa.reportBridgedCall(call.ID, (*calllog.Log).Decline)
	return nil
}
