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
	"math"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2/callbridge"
)

/*
 * The WhatsApp call's audio, over a real WebRTC connection.
 *
 * Everything else in these tests checks a stage in isolation, which is how the arithmetic gets
 * pinned down but not how a call is proved. This one stands up two actual PeerConnections on the
 * loopback interface and runs audio between them: real ICE, real DTLS, real SRTP, real Opus in real
 * RTP, through exactly the objects the bridge uses.
 *
 * What it cannot do is talk to WhatsApp, which needs a second phone number. So the far side of the
 * bridge is played by the PCM that meowcaller would have handed over, and the Matrix side is a peer
 * that answers like a client would. Everything between those two points is the real thing.
 */

// loopbackSettings keeps ICE on 127.0.0.1 with no mDNS, so the test needs no network.
func loopbackSettings() *webrtc.SettingEngine {
	se := &webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetInterfaceFilter(func(name string) bool { return name == "lo" || name == "lo0" })
	return se
}

func newMediaTestLeg(t *testing.T, name string) *callbridge.Leg {
	t.Helper()
	leg, err := callbridge.NewLeg(callbridge.LegConfig{
		Name: name, OpusPT: 111, Settings: loopbackSettings(), Log: zerolog.Nop(),
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	t.Cleanup(leg.Close)
	return leg
}

// connectLegs negotiates offerer -> answerer the way the bridge and a Matrix client would, with the
// candidates already in the SDP rather than trickled.
func connectLegs(t *testing.T, ctx context.Context, offerer, answerer *callbridge.Leg) {
	t.Helper()
	if _, err := offerer.CreateOffer(); err != nil {
		t.Fatalf("create offer: %v", err)
	}
	offer := offerer.WaitGathering(ctx, 5*time.Second)
	if offer == "" {
		t.Fatal("the offerer gathered no candidates")
	}
	if _, err := answerer.AnswerOffer(offer); err != nil {
		t.Fatalf("answer offer: %v", err)
	}
	answer := answerer.WaitGathering(ctx, 5*time.Second)
	if answer == "" {
		t.Fatal("the answerer gathered no candidates")
	}
	if err := offerer.SetAnswer(answer); err != nil {
		t.Fatalf("set answer: %v", err)
	}
}

func TestWhatsAppAudioReachesMatrixOverARealConnection(t *testing.T) {
	/*
	 * The end-to-end test.
	 *
	 * A tone goes in as the 16 kHz PCM meowcaller produces, crosses a real PeerConnection as Opus,
	 * and is measured on the far side after being turned back into 16 kHz PCM. If any stage is
	 * wrong in a way the unit tests miss - a payload type nothing accepts, packets that the SRTP
	 * layer rejects, frames the far end cannot decode - nothing arrives and this fails.
	 */
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bridge := newMediaTestLeg(t, "bridge")
	peer := newMediaTestLeg(t, "matrix-client")
	connectLegs(t, ctx, bridge, peer)

	toMatrix, err := newWAToMatrix()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	backToWA, err := newMatrixToWA()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}

	// What meowcaller would hand over: 60 ms frames of 16 kHz PCM.
	frame := tone(1000, waRate, waFrame)
	go func() {
		ticker := time.NewTicker(60 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			packets, err := toMatrix.Frame(frame)
			if err != nil {
				return
			}
			for _, packet := range packets {
				if err = bridge.Local.WriteRTP(packet); err != nil {
					return
				}
			}
		}
	}()

	track, err := peer.RemoteTrack(ctx)
	if err != nil {
		t.Fatalf("the Matrix side never received a track: %v", err)
	}

	// Collect enough frames to be past the encoder's warm-up and the connection's first packets.
	var frames [][]float32
	deadline := time.Now().Add(20 * time.Second)
	for len(frames) < 12 && time.Now().Before(deadline) {
		packet, _, err := track.ReadRTP()
		if err != nil {
			t.Fatalf("reading the relayed audio failed after %d frames: %v", len(frames), err)
		}
		got, err := backToWA.Packet(packet.Payload)
		if err != nil {
			t.Fatalf("decoding the relayed audio failed: %v", err)
		}
		frames = append(frames, got...)
	}
	if len(frames) < 12 {
		t.Fatalf("only %d frames crossed the connection in 20s", len(frames))
	}

	last := frames[len(frames)-1]
	if len(last) != waFrame {
		t.Errorf("a frame came back as %d samples, want %d", len(last), waFrame)
	}
	got, want := rms(last), rms(frame)
	if math.Abs(got-want)/want > 0.35 {
		t.Errorf("the tone crossed the connection at rms %.4f, want about %.4f", got, want)
	}
}

func TestMatrixAudioReachesWhatsAppOverARealConnection(t *testing.T) {
	// The same path in the direction a Matrix user's voice takes: Opus off the wire, decoded and
	// resampled into the 60 ms 16 kHz frames meowcaller plays into the call.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bridge := newMediaTestLeg(t, "bridge")
	peer := newMediaTestLeg(t, "matrix-client")
	connectLegs(t, ctx, bridge, peer)

	// The peer encodes like a Matrix client: 20 ms Opus frames at 48 kHz.
	encoder, err := newOpusEncoder()
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	packetiser := newOpusPacketiser()
	source := tone(1000, mxRate, opusFrame)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			payload, err := encoder.Encode(source)
			if err != nil {
				return
			}
			packet := packetiser.Next(payload)
			if len(payload) == 0 {
				continue
			}
			packet.Payload = append([]byte(nil), payload...)
			if err = peer.Local.WriteRTP(packet); err != nil {
				return
			}
		}
	}()

	pump, err := newMatrixToWA()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	track, err := bridge.RemoteTrack(ctx)
	if err != nil {
		t.Fatalf("the bridge never received a track: %v", err)
	}

	pipe := newPCMPipe()
	var frames int
	deadline := time.Now().Add(20 * time.Second)
	for frames < 12 && time.Now().Before(deadline) {
		packet, _, err := track.ReadRTP()
		if err != nil {
			t.Fatalf("reading the Matrix audio failed after %d frames: %v", frames, err)
		}
		got, err := pump.Packet(packet.Payload)
		if err != nil {
			t.Fatalf("decoding the Matrix audio failed: %v", err)
		}
		for _, f := range got {
			pipe.Push(f)
			frames++
		}
	}
	if frames < 12 {
		t.Fatalf("only %d frames crossed the connection in 20s", frames)
	}

	// And what meowcaller would read is a real frame, not the pipe's silence filler.
	played, err := pipe.ReadFrame()
	if err != nil {
		t.Fatalf("reading from the pipe: %v", err)
	}
	if len(played) != waFrame {
		t.Fatalf("the pipe yielded %d samples, want %d", len(played), waFrame)
	}
	if rms(played) < 0.05 {
		t.Errorf("what would be played into the call is near silence (rms %.4f); the audio did not survive", rms(played))
	}
}
