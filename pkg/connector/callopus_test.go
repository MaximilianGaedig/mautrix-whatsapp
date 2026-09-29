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
	"math"
	"testing"
)

func TestOpusRoundTripKeepsTheTone(t *testing.T) {
	// The whole point of the transcoder: what WhatsApp sent has to still be recognisable after
	// being compressed for Matrix. Opus is lossy, so this asks whether a 1 kHz tone survives as a
	// 1 kHz tone at roughly its own loudness - not whether the samples match.
	enc, err := newOpusEncoder()
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	dec, err := newOpusDecoder()
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}

	in := tone(1000, mxRate, opusFrame)
	var out []float32
	// Opus has a warm-up: the first frames come back quieter while the encoder settles, so the
	// frame that gets measured is one from after that.
	for range 10 {
		payload, err := enc.Encode(in)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if len(payload) == 0 {
			continue // DTX decided this frame was silence; a pure tone should not stay there long.
		}
		if out, err = dec.Decode(payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	if len(out) != opusFrame {
		t.Fatalf("decoded %d samples, want %d", len(out), opusFrame)
	}
	got, want := rms(out), rms(in)
	if math.Abs(got-want)/want > 0.3 {
		t.Errorf("1 kHz came back at rms %.4f, want about %.4f", got, want)
	}
}

func TestOpusRejectsAFrameOfTheWrongLength(t *testing.T) {
	// libopus accepts only its own frame sizes, and a mis-sized frame is a silent corruption rather
	// than an error further down, so it is caught here where the size is still meaningful.
	enc, err := newOpusEncoder()
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	if _, err := enc.Encode(make([]float32, opusFrame+1)); err == nil {
		t.Error("a frame one sample too long was accepted")
	}
}

func TestOpusConcealsALostPacket(t *testing.T) {
	// A dropped packet has to be handed to the decoder as a gap rather than skipped: its state is
	// predictive, so silently jumping over a frame damages the next real one too.
	dec, err := newOpusDecoder()
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	out, err := dec.Conceal()
	if err != nil {
		t.Fatalf("conceal: %v", err)
	}
	if len(out) != opusFrame {
		t.Errorf("concealment produced %d samples, want %d", len(out), opusFrame)
	}
}

func TestPacketiserNumbersTheStreamMonotonically(t *testing.T) {
	p := newOpusPacketiser()
	first := p.Next([]byte{1})
	second := p.Next([]byte{2})
	third := p.Next([]byte{3})

	if !first.Marker {
		t.Error("the first packet of the stream should be marked")
	}
	if second.Marker || third.Marker {
		t.Error("only the first packet should be marked; a marker on every frame resets the jitter buffer")
	}
	if second.SequenceNumber != first.SequenceNumber+1 || third.SequenceNumber != second.SequenceNumber+1 {
		t.Errorf("sequence numbers not consecutive: %d, %d, %d",
			first.SequenceNumber, second.SequenceNumber, third.SequenceNumber)
	}
	// The RTP clock is 48 kHz by definition for Opus, so one 20 ms frame is 960 ticks.
	if got := second.Timestamp - first.Timestamp; got != opusFrame {
		t.Errorf("timestamp advanced by %d per frame, want %d", got, opusFrame)
	}
}

func TestPacketiserDoesNotStartFromZero(t *testing.T) {
	/*
	 * RFC 3550 asks for a random starting sequence number and timestamp. Starting from zero is the
	 * easy mistake and it is invisible in a call that works: it just makes the stream trivially
	 * predictable. Two packetisers agreeing exactly would mean the randomisation is not happening.
	 */
	a, b := newOpusPacketiser().Next(nil), newOpusPacketiser().Next(nil)
	if a.SequenceNumber == b.SequenceNumber && a.Timestamp == b.Timestamp {
		t.Error("two streams started at the same place; the start should be random")
	}
}
