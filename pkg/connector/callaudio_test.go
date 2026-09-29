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

// tone builds n samples of a sine at hz, sampled at rate.
func tone(hz, rate float64, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(math.Sin(2 * math.Pi * hz * float64(i) / rate))
	}
	return out
}

// rms is the loudness of a frame, which is what the filter tests are really asking about.
func rms(in []float32) float64 {
	if len(in) == 0 {
		return 0
	}
	var sum float64
	for _, s := range in {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(in)))
}

func TestUpsamplerProducesThreeSamplesForOne(t *testing.T) {
	out := newUpsampler().Frame(make([]float32, waFrame))
	if len(out) != waFrame*ratio {
		t.Fatalf("got %d samples, want %d", len(out), waFrame*ratio)
	}
	if len(out) != mxFrame*3 {
		t.Errorf("one WhatsApp frame should be exactly three Matrix frames, got %d", len(out)/mxFrame)
	}
}

func TestDownsamplerProducesOneSampleForThree(t *testing.T) {
	out := newDownsampler().Frame(make([]float32, mxFrame*3))
	if len(out) != waFrame {
		t.Fatalf("three Matrix frames should make one WhatsApp frame of %d, got %d", waFrame, len(out))
	}
}

func TestResamplingKeepsALevelSignalAtItsLevel(t *testing.T) {
	// A constant is the simplest thing a filter can get wrong: a kernel that is not normalised
	// quietly scales everything, which is inaudible in a test that only looks at frame lengths.
	const level = 0.5
	in := make([]float32, waFrame)
	for i := range in {
		in[i] = level
	}
	up := newUpsampler()
	var last []float32
	// Run several frames so the filter's history is primed and the edge is behind us.
	for range 4 {
		last = up.Frame(in)
	}
	got := float64(last[len(last)/2])
	if math.Abs(got-level) > 0.02 {
		t.Errorf("a constant came back at %.4f, want %.4f", got, level)
	}
}

func TestSpeechBandSurvivesTheRoundTrip(t *testing.T) {
	// 1 kHz is in the middle of what a voice call carries; it must come back at roughly its own
	// loudness after going up to 48 kHz and back down.
	up, down := newUpsampler(), newDownsampler()
	in := tone(1000, waRate, waFrame)
	var out []float32
	for range 4 {
		out = down.Frame(up.Frame(in))
	}
	got, want := rms(out), rms(in)
	if math.Abs(got-want)/want > 0.15 {
		t.Errorf("1 kHz came back at rms %.4f, want about %.4f", got, want)
	}
}

func TestATooHighToneIsRemovedRatherThanFoldedDown(t *testing.T) {
	/*
	 * The test this file exists for.
	 *
	 * 12 kHz cannot be represented at 16 kHz - its Nyquist is 8 kHz - so decimating without
	 * filtering first does not lose it, it *moves* it: it comes back as 4 kHz, a whine sitting in
	 * the middle of the speech band that was never in the call. Filtering first removes it instead,
	 * so what arrives is near silence rather than a loud wrong note.
	 */
	down := newDownsampler()
	in := tone(12000, mxRate, mxFrame*3)
	var out []float32
	for range 4 {
		out = down.Frame(in)
	}
	if level := rms(out); level > 0.1 {
		t.Errorf("a 12 kHz tone came through at rms %.4f; it should be attenuated, not aliased", level)
	}
}

func TestFramerCutsThreeMatrixFramesIntoOneWhatsAppFrame(t *testing.T) {
	// The framer sits after the downsampler, so what it receives is one 20 ms Matrix frame's worth
	// of 16 kHz samples - 960 at 48 kHz becomes 320 - and three of those make a 60 ms frame.
	const downsampled = mxFrame / ratio
	f := newFramer(waFrame)

	if got := f.Push(make([]float32, downsampled)); len(got) != 0 {
		t.Errorf("20 ms in, got %d frames out, want none yet", len(got))
	}
	if got := f.Push(make([]float32, downsampled)); len(got) != 0 {
		t.Errorf("40 ms in, got %d frames out, want none yet", len(got))
	}
	// The third completes exactly one 60 ms frame, with nothing left over.
	got := f.Push(make([]float32, downsampled))
	if len(got) != 1 || len(got[0]) != waFrame {
		t.Fatalf("60 ms in, got %d frames out", len(got))
	}
	if f.Pending() != 0 {
		t.Errorf("%d samples held back, want none", f.Pending())
	}
}

func TestFramerHoldsThePartialRemainder(t *testing.T) {
	f := newFramer(waFrame)
	// An odd-sized push must not be padded or dropped: the leftover belongs to the next frame.
	f.Push(make([]float32, waFrame+100))
	if f.Pending() != 100 {
		t.Errorf("held %d samples, want 100", f.Pending())
	}
	if got := f.Push(make([]float32, waFrame-100)); len(got) != 1 {
		t.Errorf("the remainder should have completed one frame, got %d", len(got))
	}
}
