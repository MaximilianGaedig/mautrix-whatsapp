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
	"io"
	"math"
	"testing"
)

func TestOneWhatsAppFrameIsThreeMatrixPackets(t *testing.T) {
	// The arithmetic the whole chain rests on. If this is wrong the call still runs, it just plays
	// at the wrong speed, which no other test in here would notice.
	w, err := newWAToMatrix()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	// A tone rather than silence: DTX legitimately suppresses silent frames, so silence would make
	// the count zero for reasons that have nothing to do with the arithmetic.
	packets, err := w.Frame(tone(1000, waRate, waFrame))
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if len(packets) != 3 {
		t.Fatalf("one 60 ms WhatsApp frame produced %d packets, want 3", len(packets))
	}
	// And they must be a stream, not three unrelated packets.
	for i := 1; i < len(packets); i++ {
		if packets[i].SequenceNumber != packets[i-1].SequenceNumber+1 {
			t.Errorf("packet %d does not follow packet %d", i, i-1)
		}
		if packets[i].Timestamp-packets[i-1].Timestamp != opusFrame {
			t.Errorf("packet %d is not one frame after packet %d", i, i-1)
		}
	}
}

func TestThreeMatrixPacketsMakeOneWhatsAppFrame(t *testing.T) {
	w, err := newWAToMatrix()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	m, err := newMatrixToWA()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	packets, err := w.Frame(tone(1000, waRate, waFrame))
	if err != nil {
		t.Fatalf("frame: %v", err)
	}

	var frames [][]float32
	for _, packet := range packets {
		got, err := m.Packet(packet.Payload)
		if err != nil {
			t.Fatalf("packet: %v", err)
		}
		frames = append(frames, got...)
	}
	if len(frames) != 1 {
		t.Fatalf("three Matrix packets produced %d WhatsApp frames, want 1", len(frames))
	}
	if len(frames[0]) != waFrame {
		t.Errorf("frame is %d samples, want %d", len(frames[0]), waFrame)
	}
}

func TestAToneSurvivesTheWholeRoundTrip(t *testing.T) {
	/*
	 * End to end, in the terms the call is actually in: 16 kHz PCM from meowcaller, up to 48 kHz,
	 * Opus, RTP, back down, and out as 16 kHz PCM. Every stage is lossy or approximate, so this
	 * asks the only question that matters - is what comes out still the tone that went in, at
	 * roughly the loudness it went in at.
	 */
	w, err := newWAToMatrix()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}
	m, err := newMatrixToWA()
	if err != nil {
		t.Fatalf("pump: %v", err)
	}

	in := tone(1000, waRate, waFrame)
	var last []float32
	// Long enough for the encoder to settle and the filters' history to fill.
	for range 12 {
		packets, err := w.Frame(in)
		if err != nil {
			t.Fatalf("frame: %v", err)
		}
		for _, packet := range packets {
			frames, err := m.Packet(packet.Payload)
			if err != nil {
				t.Fatalf("packet: %v", err)
			}
			if len(frames) > 0 {
				last = frames[len(frames)-1]
			}
		}
	}
	if last == nil {
		t.Fatal("nothing came out the far end")
	}
	got, want := rms(last), rms(in)
	if math.Abs(got-want)/want > 0.35 {
		t.Errorf("the tone came back at rms %.4f, want about %.4f", got, want)
	}
}

func TestPipeDropsRatherThanGrows(t *testing.T) {
	/*
	 * The decision this type exists to make.
	 *
	 * Matrix delivers when the network delivers; WhatsApp asks every 60 ms. If the pipe grew
	 * instead of dropping, a call that fell behind would stay behind forever - latency does not
	 * come back on its own - and the backlog would keep climbing for as long as the call lasted.
	 */
	p := newPCMPipe()
	for range pcmPipeDepth + 5 {
		p.Push(make([]float32, waFrame))
	}
	if p.Depth() != pcmPipeDepth {
		t.Errorf("pipe holds %d frames, want it capped at %d", p.Depth(), pcmPipeDepth)
	}
	if p.Dropped != 5 {
		t.Errorf("dropped %d frames, want 5", p.Dropped)
	}
}

func TestPipeKeepsTheNewestAudio(t *testing.T) {
	// When it drops, it has to drop the oldest: the stale end of a backlog is the part nobody wants
	// to hear, and keeping it would play the conversation late as well as skipping.
	p := newPCMPipe()
	for i := range pcmPipeDepth + 1 {
		frame := make([]float32, waFrame)
		frame[0] = float32(i)
		p.Push(frame)
	}
	first, err := p.ReadFrame()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if first[0] != 1 {
		t.Errorf("the pipe kept frame %v after overflowing; it should have dropped frame 0", first[0])
	}
}

func TestPipeYieldsSilenceRatherThanStalling(t *testing.T) {
	// WhatsApp's sender has its own clock and has to put something on the wire every 60 ms. An
	// empty pipe that blocked would stall that clock, which is a worse artefact than silence.
	p := newPCMPipe()
	frame, err := p.ReadFrame()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(frame) != waFrame {
		t.Errorf("an empty pipe gave %d samples, want %d of silence", len(frame), waFrame)
	}
	for _, s := range frame {
		if s != 0 {
			t.Fatal("the filler frame is not silent")
		}
	}
}

func TestPipeEndsWhenClosed(t *testing.T) {
	// Closing has to surface as io.EOF, because that is how meowcaller's Player is told a source is
	// finished; anything else leaves the player running against a dead pipe for the rest of the call.
	p := newPCMPipe()
	p.Push(make([]float32, waFrame))
	if err := p.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := p.ReadFrame(); err != io.EOF {
		t.Errorf("a closed pipe returned %v, want io.EOF", err)
	}
}
