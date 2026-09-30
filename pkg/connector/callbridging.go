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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pion/webrtc/v4"
	"github.com/purpshell/meowcaller"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/callbridge"
	"maunium.net/go/mautrix/bridgev2/calllog"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

/*
 * A WhatsApp call, as a Matrix call.
 *
 * The shape follows mautrix-signal's call bridge, because the Matrix half of the problem is the
 * same: m.call.* is not something bridgev2 delivers on its own, so the event processor is hooked
 * directly, one call at a time is bridged, and the leg is driven from the ghost of whoever is on
 * the other end.
 *
 * The WhatsApp half is not the same. Signal and Messenger hand over RTP that can be forwarded as it
 * stands; WhatsApp hands over decoded PCM, so between the two legs sit the transcoding chains in
 * callpump.go rather than a relay. That difference is the reason this file exists separately rather
 * than sharing Signal's.
 */

// waCallInviteLifetime is how long Matrix is told the invite is good for. WhatsApp stops ringing on
// its own at about the same point.
const waCallInviteLifetime = 60 * time.Second

var waMatrixCallEventTypes = []event.Type{
	event.CallInvite, event.CallCandidates, event.CallAnswer, event.CallReject,
	event.CallSelectAnswer, event.CallNegotiate, event.CallHangup,
}

// registerCallEventHandlers hooks m.call.* into the Matrix event processor, which bridgev2 itself
// drops. The same hook mautrix-meta and mautrix-signal use.
func (wa *WhatsAppConnector) registerCallEventHandlers() {
	if !wa.Config.CallBridging {
		return
	}
	mx, ok := wa.Bridge.Matrix.(*matrix.Connector)
	if !ok || mx.EventProcessor == nil {
		wa.Bridge.Log.Warn().Msg("Matrix connector doesn't expose an event processor, call bridging can't receive m.call events")
		return
	}
	for _, evtType := range waMatrixCallEventTypes {
		mx.EventProcessor.On(evtType, wa.handleMatrixCallEvent)
	}
}

func (wa *WhatsAppConnector) handleMatrixCallEvent(ctx context.Context, evt *event.Event) {
	if !wa.Config.CallBridging || wa.Bridge.IsGhostMXID(evt.Sender) || evt.Sender == wa.Bridge.Bot.GetMXID() {
		return
	}
	log := wa.Bridge.Log.With().
		Str("action", "handle matrix call event").
		Str("event_type", evt.Type.Type).
		Stringer("event_id", evt.ID).
		Stringer("room_id", evt.RoomID).
		Logger()
	ctx = log.WithContext(ctx)
	portal, err := wa.Bridge.GetPortalByMXID(ctx, evt.RoomID)
	if err != nil || portal == nil {
		return
	}
	login, err := wa.Bridge.GetExistingUserLoginByID(ctx, portal.Receiver)
	if err != nil || login == nil {
		user, userErr := wa.Bridge.GetUserByMXID(ctx, evt.Sender)
		if userErr != nil || user == nil {
			return
		}
		login = user.GetDefaultLogin()
	}
	if login == nil || login.UserMXID != evt.Sender {
		log.Debug().Msg("Ignoring call event from a user without a login for this portal")
		return
	}
	client, ok := login.Client.(*WhatsAppClient)
	if !ok || client.Calls == nil {
		return
	}
	client.Calls.handleMatrixEvent(ctx, portal, evt)
}

// waCallBridge holds the one call a login can have bridged at a time.
//
// One at a time rather than a map, because WhatsApp itself only rings one call at a time per
// device: a second offer while one is up is something to decline, not something to queue.
type waCallBridge struct {
	client *WhatsAppClient
	mc     *meowcaller.Client
	lock   sync.Mutex
	active *waCallSession
}

type waCallSession struct {
	bridge *waCallBridge
	ctx    context.Context
	cancel context.CancelFunc
	log    zerolog.Logger
	lock   sync.Mutex

	portal *bridgev2.Portal
	ghost  bridgev2.MatrixAPI
	call   *meowcaller.Call

	incoming bool
	mxCallID string
	mxParty  string
	mxLeg    *callbridge.Leg

	answering bool
	mediaOnce sync.Once
	endOnce   sync.Once
}

// newCallBridge wires the calling stack onto a connected whatsmeow client.
//
// meowcaller has to be constructed before whatsmeow connects: it intercepts the raw <call> and
// <ack> stanzas, and an interceptor installed after the receive loop is running misses whatever
// arrived in between.
func newCallBridge(client *WhatsAppClient) *waCallBridge {
	cb := &waCallBridge{client: client}
	cb.mc = meowcaller.NewClient(client.Client, meowcaller.WithLogger(
		client.UserLogin.Log.With().Str("component", "meowcaller").Logger(),
	))
	cb.mc.OnIncomingCall(func(call *meowcaller.Call) {
		go cb.startIncoming(client.UserLogin.Log.WithContext(context.Background()), call)
	})
	return cb
}

/*
 * Incoming: WhatsApp is ringing, so Matrix should ring.
 */
func (cb *waCallBridge) startIncoming(ctx context.Context, call *meowcaller.Call) {
	cb.lock.Lock()
	busy := cb.active != nil
	cb.lock.Unlock()
	if busy {
		// Declining rather than queueing: WhatsApp rings one call at a time, and a caller who is
		// left with no answer at all cannot tell the difference between busy and broken.
		if err := call.Reject(); err != nil {
			cb.client.UserLogin.Log.Warn().Err(err).Msg("Failed to reject second WhatsApp call")
		}
		return
	}

	peer := call.Peer()
	portal, err := cb.client.Main.Bridge.GetExistingPortalByKey(ctx, cb.client.makeWAPortalKey(peer))
	if err != nil || portal == nil || portal.MXID == "" {
		cb.client.UserLogin.Log.Warn().Err(err).Msg("Can't ring Matrix for WhatsApp call without an existing portal")
		return
	}
	ghost, err := cb.client.Main.Bridge.GetGhostByID(ctx, waid.MakeUserID(peer))
	if err != nil || ghost == nil {
		cb.client.UserLogin.Log.Warn().Err(err).Msg("Can't get WhatsApp caller ghost")
		return
	}

	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	session := &waCallSession{
		bridge: cb, ctx: sessionCtx, cancel: cancel,
		portal: portal, ghost: ghost.Intent, call: call, incoming: true,
		mxCallID: uuid.NewString(), mxParty: "bridge-" + uuid.NewString()[:8],
	}
	session.log = cb.client.UserLogin.Log.With().
		Str("component", "call bridge").
		Str("wa_call_id", call.ID()).
		Str("portal_id", string(portal.ID)).
		Str("mx_call_id", session.mxCallID).
		Logger()

	cb.lock.Lock()
	if cb.active != nil {
		cb.lock.Unlock()
		cancel()
		_ = call.Reject()
		return
	}
	cb.active = session
	cb.lock.Unlock()

	session.watchCall()
	if err = session.ringMatrix(); err != nil {
		session.log.Error().Err(err).Msg("Failed to ring Matrix for incoming WhatsApp call")
		session.end(true)
		return
	}
	session.log.Info().Msg("Ringing Matrix for incoming WhatsApp call")
	time.AfterFunc(waCallInviteLifetime, func() {
		if session.ctx.Err() == nil && !session.isAnswering() {
			session.log.Info().Msg("Nobody answered the WhatsApp call in Matrix")
			session.end(true)
		}
	})
}

// watchCall follows the WhatsApp side of the call, so that anything that ends it there ends it here.
func (s *waCallSession) watchCall() {
	s.call.OnEnd(func(reason string) {
		s.log.Info().Str("reason", reason).Msg("WhatsApp ended the call")
		// notifyWA is false: WhatsApp is the one telling us, so hanging up at it would be answering
		// a goodbye with a goodbye.
		s.end(false)
	})
	s.call.OnReady(func() {
		s.log.Info().Msg("WhatsApp call media is ready")
		s.startMedia()
	})
	s.call.OnPeerAccept(func() {
		s.log.Info().Msg("WhatsApp peer accepted the call")
	})
}

func (s *waCallSession) matrixICEServers() []webrtc.ICEServer {
	asIntent, ok := s.ghost.(*matrix.ASIntent)
	if !ok {
		return nil
	}
	resp, err := asIntent.Matrix.TurnServer(s.ctx)
	if err != nil {
		s.log.Warn().Err(err).Msg("Failed to fetch homeserver TURN server, using no relay on the Matrix leg")
		return nil
	} else if len(resp.URIs) == 0 {
		return nil
	}
	return []webrtc.ICEServer{{URLs: resp.URIs, Username: resp.Username, Credential: resp.Password}}
}

// newMatrixLeg builds the Matrix side of the call.
//
// No VideoCodec: a WhatsApp call's video is H.264 that meowcaller hands over as access units rather
// than RTP, so it cannot be relayed the way Signal's is, and offering video that never arrives is
// worse than offering audio only.
func (s *waCallSession) newMatrixLeg() (*callbridge.Leg, error) {
	return callbridge.NewLeg(callbridge.LegConfig{
		Name: "matrix", ICEServers: s.matrixICEServers(), Log: s.log,
	})
}

func (s *waCallSession) ringMatrix() error {
	leg, err := s.newMatrixLeg()
	if err != nil {
		return err
	}
	s.lock.Lock()
	if s.ctx.Err() != nil {
		s.lock.Unlock()
		leg.Close()
		return s.ctx.Err()
	}
	s.mxLeg = leg
	s.lock.Unlock()

	if _, err = leg.CreateOffer(); err != nil {
		leg.Close()
		return err
	}
	offer := leg.WaitGathering(s.ctx, 3*time.Second)
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	_, err = s.ghost.SendMessage(s.ctx, s.portal.MXID, event.CallInvite, waCallEventContent(&event.CallInviteEventContent{
		BaseCallEventContent: s.baseMatrixContent(),
		Lifetime:             int(waCallInviteLifetime / time.Millisecond),
		Offer:                event.CallData{SDP: offer, Type: event.CallDataTypeOffer},
	}), nil)
	return err
}

func (s *waCallSession) baseMatrixContent() event.BaseCallEventContent {
	return event.BaseCallEventContent{CallID: s.mxCallID, PartyID: s.mxParty, Version: event.CallVersion("1")}
}

func waCallEventContent(parsed any) *event.Content {
	return &event.Content{Parsed: parsed}
}

func parseWACallContent[T any](evt *event.Event) (*T, bool) {
	parsed, ok := evt.Content.Parsed.(*T)
	return parsed, ok
}

/*
 * Matrix events.
 */
func (cb *waCallBridge) handleMatrixEvent(ctx context.Context, portal *bridgev2.Portal, evt *event.Event) {
	if evt.Type == event.CallInvite {
		if inv, ok := parseWACallContent[event.CallInviteEventContent](evt); ok {
			go cb.startOutgoing(context.WithoutCancel(ctx), portal, inv)
		}
		return
	}
	var base *event.BaseCallEventContent
	if evt.Type == event.CallAnswer {
		if answer, ok := parseWACallContent[event.CallAnswerEventContent](evt); ok {
			base = &answer.BaseCallEventContent
		}
	} else if parsed, ok := parseWACallContent[event.BaseCallEventContent](evt); ok {
		base = parsed
	}
	if base == nil {
		return
	}
	cb.lock.Lock()
	session := cb.active
	cb.lock.Unlock()
	if session == nil || session.mxCallID != base.CallID || session.portal.MXID != portal.MXID || base.PartyID == session.mxParty {
		return
	}
	switch evt.Type {
	case event.CallReject, event.CallHangup:
		session.log.Info().Str("event_type", evt.Type.Type).Msg("Matrix user ended the WhatsApp call")
		go session.end(true)
	case event.CallAnswer:
		if !session.incoming {
			return
		}
		answer, ok := parseWACallContent[event.CallAnswerEventContent](evt)
		if !ok || answer.Answer.SDP == "" {
			go session.end(true)
			return
		}
		go session.answerIncoming(answer.Answer.SDP, answer.PartyID)
	case event.CallCandidates:
		if candidates, ok := parseWACallContent[event.CallCandidatesEventContent](evt); ok {
			session.addMatrixCandidates(candidates.Candidates)
		}
	}
}

func (s *waCallSession) addMatrixCandidates(candidates []event.CallCandidate) {
	s.lock.Lock()
	leg := s.mxLeg
	s.lock.Unlock()
	if leg == nil {
		return
	}
	for _, candidate := range candidates {
		if candidate.SDPMLineIndex < 0 || candidate.SDPMLineIndex > 65535 {
			s.log.Debug().Msg("Rejected Matrix ICE candidate with invalid media line index")
			continue
		}
		mid := candidate.SDPMID
		line := uint16(candidate.SDPMLineIndex)
		if err := leg.AddCandidate(webrtc.ICECandidateInit{
			Candidate: candidate.Candidate, SDPMid: &mid, SDPMLineIndex: &line,
		}); err != nil {
			// Candidates carry addresses, so the error is not logged with them.
			s.log.Debug().Msg("Rejected malformed Matrix ICE candidate")
		}
	}
}

func (s *waCallSession) isAnswering() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.answering
}

// answerIncoming is the Matrix user picking up a WhatsApp call.
func (s *waCallSession) answerIncoming(matrixAnswer, matrixParty string) {
	s.lock.Lock()
	if s.answering || s.ctx.Err() != nil {
		s.lock.Unlock()
		return
	}
	s.answering = true
	mxLeg := s.mxLeg
	s.lock.Unlock()
	if mxLeg == nil {
		s.end(true)
		return
	}
	if err := mxLeg.SetAnswer(matrixAnswer); err != nil {
		s.log.Warn().Msg("Rejected malformed Matrix call answer")
		s.end(true)
		return
	}
	if _, err := s.ghost.SendMessage(s.ctx, s.portal.MXID, event.CallSelectAnswer, waCallEventContent(&event.CallSelectAnswerEventContent{
		BaseCallEventContent: s.baseMatrixContent(), SelectedPartyID: matrixParty,
	}), nil); err != nil {
		s.log.Warn().Err(err).Msg("Failed to select Matrix call answer")
	}
	// Answering is what makes WhatsApp start media; OnReady then starts the pumps.
	if err := s.call.Answer(); err != nil {
		s.log.Error().Err(err).Msg("Failed to answer the WhatsApp call")
		s.end(true)
		return
	}
	s.bridge.client.reportBridgedCall(s.call.ID(), (*calllog.Log).Answer)
}

/*
 * Outgoing: a Matrix user placed the call.
 */
func (cb *waCallBridge) startOutgoing(ctx context.Context, portal *bridgev2.Portal, inv *event.CallInviteEventContent) {
	if portal.OtherUserID == "" || inv == nil || inv.Offer.SDP == "" {
		return
	}
	cb.lock.Lock()
	busy := cb.active != nil
	cb.lock.Unlock()
	if busy {
		return
	}
	peer := waid.ParseUserID(portal.OtherUserID)
	if peer.IsEmpty() {
		return
	}
	ghost, err := cb.client.Main.Bridge.GetGhostByID(ctx, portal.OtherUserID)
	if err != nil || ghost == nil {
		return
	}

	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	session := &waCallSession{
		bridge: cb, ctx: sessionCtx, cancel: cancel,
		portal: portal, ghost: ghost.Intent,
		mxCallID: inv.CallID, mxParty: "bridge-" + uuid.NewString()[:8],
	}
	session.log = cb.client.UserLogin.Log.With().
		Str("component", "call bridge").
		Str("portal_id", string(portal.ID)).
		Str("mx_call_id", session.mxCallID).
		Logger()

	cb.lock.Lock()
	if cb.active != nil {
		cb.lock.Unlock()
		cancel()
		return
	}
	cb.active = session
	cb.lock.Unlock()

	leg, err := session.newMatrixLeg()
	if err != nil {
		session.log.Error().Err(err).Msg("Failed to create the Matrix leg for an outgoing call")
		session.end(false)
		return
	}
	session.lock.Lock()
	session.mxLeg = leg
	session.lock.Unlock()

	if _, err = leg.AnswerOffer(inv.Offer.SDP); err != nil {
		session.log.Warn().Msg("Rejected malformed Matrix call offer")
		session.end(false)
		return
	}
	answer := leg.WaitGathering(sessionCtx, 3*time.Second)
	if sessionCtx.Err() != nil {
		return
	}

	call, err := cb.mc.Call(sessionCtx, peer.String())
	if err != nil {
		session.log.Error().Err(err).Msg("Failed to place the WhatsApp call")
		session.end(false)
		return
	}
	session.lock.Lock()
	session.call = call
	session.lock.Unlock()
	session.watchCall()

	/*
	 * The answer goes to Matrix now, not when WhatsApp picks up.
	 *
	 * Matrix has no "it is ringing over there" state for 1:1 calls, and holding the answer back
	 * until the peer accepts leaves the caller's client waiting on an invite that looks unanswered.
	 * Media simply does not flow until OnReady fires, which is the same thing the caller hears.
	 */
	if _, err = session.ghost.SendMessage(sessionCtx, portal.MXID, event.CallAnswer, waCallEventContent(&event.CallAnswerEventContent{
		BaseCallEventContent: session.baseMatrixContent(),
		Answer:               event.CallData{SDP: answer, Type: event.CallDataTypeAnswer},
	}), nil); err != nil {
		session.log.Error().Err(err).Msg("Failed to answer the Matrix call")
		session.end(true)
		return
	}
	session.log.Info().Msg("Placed a WhatsApp call for an outgoing Matrix call")
}

/*
 * Media.
 *
 * Both directions start together, once, when WhatsApp says its media is ready. The Matrix leg may
 * still be connecting at that point, which is fine: reading its remote track blocks until there is
 * one, and writing to a track nobody is receiving yet is discarded rather than an error.
 */
func (s *waCallSession) startMedia() {
	s.mediaOnce.Do(func() {
		go s.pumpWAToMatrix()
		go s.pumpMatrixToWA()
	})
}

func (s *waCallSession) pumpWAToMatrix() {
	pump, err := newWAToMatrix()
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to create the WhatsApp to Matrix audio pump")
		s.end(true)
		return
	}
	s.lock.Lock()
	leg := s.mxLeg
	s.lock.Unlock()
	if leg == nil {
		return
	}
	// Receive hands frames over on meowcaller's own goroutine, so the work here has to stay short:
	// it is 60 ms of audio through a 31-tap filter and three Opus frames, which is microseconds.
	s.call.Receive(meowcaller.SinkFunc(func(frame []float32) {
		if s.ctx.Err() != nil {
			return
		}
		packets, err := pump.Frame(frame)
		if err != nil {
			s.log.Warn().Err(err).Msg("Failed to encode WhatsApp audio for Matrix")
			return
		}
		for _, packet := range packets {
			if err = leg.Local.WriteRTP(packet); err != nil {
				s.log.Debug().Err(err).Msg("Failed to write audio to the Matrix leg")
				return
			}
		}
	}))
}

func (s *waCallSession) pumpMatrixToWA() {
	pump, err := newMatrixToWA()
	if err != nil {
		s.log.Error().Err(err).Msg("Failed to create the Matrix to WhatsApp audio pump")
		s.end(true)
		return
	}
	s.lock.Lock()
	leg := s.mxLeg
	s.lock.Unlock()
	if leg == nil {
		return
	}
	track, err := leg.RemoteTrack(s.ctx)
	if err != nil {
		if s.ctx.Err() == nil {
			s.log.Warn().Err(err).Msg("No remote audio track on the Matrix leg")
		}
		return
	}

	pipe := newPCMPipe()
	defer pipe.Close()
	s.call.Play(pipe)

	// Sequence numbers are tracked to tell a lost packet from an ordinary one: the decoder's state
	// is predictive, so a gap has to be declared rather than skipped over.
	var lastSequence uint16
	var started bool
	for s.ctx.Err() == nil {
		packet, _, err := track.ReadRTP()
		if err != nil {
			if s.ctx.Err() == nil {
				s.log.Debug().Err(err).Msg("Matrix audio track ended")
			}
			return
		}
		if started {
			// Only small forward gaps are concealed. A large one is a reordering or a restart
			// rather than loss, and concealing thousands of frames would be worse than the gap.
			for gap := packet.SequenceNumber - lastSequence - 1; gap > 0 && gap < 10; gap-- {
				frames, concealErr := pump.Conceal()
				if concealErr != nil {
					break
				}
				for _, frame := range frames {
					pipe.Push(frame)
				}
			}
		}
		lastSequence, started = packet.SequenceNumber, true

		frames, err := pump.Packet(packet.Payload)
		if err != nil {
			s.log.Debug().Err(err).Msg("Failed to decode Matrix audio")
			continue
		}
		for _, frame := range frames {
			pipe.Push(frame)
		}
	}
}

/*
 * Ending.
 *
 * Every path ends here, and it runs once: a call can be ended from either side at the same moment,
 * and hanging up twice at WhatsApp turns a normal end into an error in the log of a call that
 * finished perfectly well.
 */
func (s *waCallSession) end(notifyWA bool) {
	s.endOnce.Do(func() {
		s.lock.Lock()
		leg, call := s.mxLeg, s.call
		answering := s.answering
		s.lock.Unlock()

		if notifyWA && call != nil {
			var err error
			if s.incoming {
				report := (*calllog.Log).End
				if !answering {
					report = (*calllog.Log).Decline
				}
				s.bridge.client.reportBridgedCall(call.ID(), report)
			}
			if s.incoming && !answering {
				// Never picked up in Matrix, so this is a decline rather than a hangup, and the
				// caller's phone should say so.
				err = call.Reject()
			} else {
				err = call.Hangup()
			}
			if err != nil {
				s.log.Debug().Err(err).Msg("Failed to end the WhatsApp call")
			}
		}
		// Matrix is told before the context is cancelled, since sending needs it.
		if _, err := s.ghost.SendMessage(s.ctx, s.portal.MXID, event.CallHangup, waCallEventContent(&event.CallHangupEventContent{
			BaseCallEventContent: s.baseMatrixContent(), Reason: event.CallHangupUserHangup,
		}), nil); err != nil {
			s.log.Debug().Err(err).Msg("Failed to hang up the Matrix call")
		}

		s.cancel()
		if leg != nil {
			leg.Close()
		}
		s.bridge.lock.Lock()
		if s.bridge.active == s {
			s.bridge.active = nil
		}
		s.bridge.lock.Unlock()
		s.log.Info().Msg("Call ended")
	})
}

// stop ends whatever is bridged, for a logout or a disconnect.
func (cb *waCallBridge) stop() {
	cb.lock.Lock()
	session := cb.active
	cb.lock.Unlock()
	if session != nil {
		session.end(true)
	}
}
