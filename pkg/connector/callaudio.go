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

import "math"

/*
 * Making WhatsApp's audio and Matrix's audio the same audio.
 *
 * The two ends do not agree on anything about how the samples are carried:
 *
 *	WhatsApp (meowcaller)   16 kHz mono, 960-sample frames  = 60 ms
 *	Matrix (Opus/WebRTC)    48 kHz mono, 960-sample frames  = 20 ms
 *
 * So every frame has to be resampled by three and re-cut: one WhatsApp frame is exactly three
 * Matrix frames, and three Matrix frames are exactly one WhatsApp frame. The arithmetic is exact,
 * which is the one mercy here - no fractional resampling, no drift to accumulate.
 *
 * This is unlike Signal and Messenger, where both ends speak Opus at 48 kHz and the bridge is a
 * pure relay that never looks at a sample. WhatsApp calls default to Meta's MLow codec, so there is
 * no shared codec to relay and the audio must be carried as PCM between the two.
 *
 * Rate conversion cannot be done by simply repeating or dropping samples. Going down, anything
 * above the new Nyquist (8 kHz) folds back into the audible band as a whine that was never there;
 * going up, the images above 8 kHz are real signal to the encoder and it spends bits on them. Both
 * directions therefore filter, with a windowed-sinc low-pass at 7.5 kHz - below 8 kHz so the
 * transition band has somewhere to live.
 */

const (
	// waRate is what meowcaller deals in: 16 kHz mono (meowcaller.SampleRate).
	waRate = 16000
	// mxRate is what Opus deals in over WebRTC.
	mxRate = 48000
	// ratio is exact, which is why nothing here has to track a fractional position.
	ratio = mxRate / waRate
	// waFrame is meowcaller's frame (meowcaller.FrameSamples), 60 ms at 16 kHz.
	waFrame = 960
	// mxFrame is one 20 ms Opus frame at 48 kHz.
	mxFrame = 960
	// cutoff is the low-pass corner, below the 8 kHz Nyquist of the 16 kHz side.
	cutoff = 7500.0
	// taps is the filter length: enough to be worth calling a filter, short enough that a frame's
	// worth of audio costs microseconds.
	taps = 31
)

// lowPass is a windowed-sinc FIR, designed once for a given rate and reused.
//
// A Hamming window is used rather than a bare sinc: truncating a sinc rings, and the ringing is
// audible as a tone alongside anything percussive.
type lowPass struct {
	kernel []float32
	// history holds the samples before the current frame, so a filtered frame joins the one before
	// it without a discontinuity - a click on every frame boundary otherwise, fifty times a second.
	history []float32
}

func newLowPass(rate float64) *lowPass {
	kernel := make([]float32, taps)
	mid := (taps - 1) / 2
	sum := 0.0
	for i := range taps {
		n := float64(i - mid)
		// The sinc itself, normalised to the sample rate.
		fc := cutoff / rate
		var v float64
		if n == 0 {
			v = 2 * fc
		} else {
			v = math.Sin(2*math.Pi*fc*n) / (math.Pi * n)
		}
		// Hamming.
		v *= 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(taps-1))
		kernel[i] = float32(v)
		sum += v
	}
	// Normalised so a constant signal passes through unchanged, which is what makes the DC test a
	// meaningful check that the filter is not quietly attenuating everything.
	for i := range kernel {
		kernel[i] = float32(float64(kernel[i]) / sum)
	}
	return &lowPass{kernel: kernel, history: make([]float32, taps-1)}
}

// apply filters one frame, carrying the tail of the previous frame across the join.
func (l *lowPass) apply(in []float32) []float32 {
	padded := make([]float32, 0, len(l.history)+len(in))
	padded = append(padded, l.history...)
	padded = append(padded, in...)

	out := make([]float32, len(in))
	for i := range in {
		var acc float32
		for k, c := range l.kernel {
			acc += c * padded[i+k]
		}
		out[i] = acc
	}
	// Keep the last taps-1 samples for the next frame's join.
	copy(l.history, padded[len(padded)-len(l.history):])
	return out
}

// upsampler turns one 16 kHz frame into the 48 kHz samples Opus wants.
type upsampler struct{ filter *lowPass }

func newUpsampler() *upsampler { return &upsampler{filter: newLowPass(mxRate)} }

// Frame returns len(in)*ratio samples at 48 kHz.
//
// Zero-stuffing then filtering is the textbook interpolation: the inserted zeros put images of the
// signal above 8 kHz, and the filter is what removes them. The gain lost to stuffing (only one
// sample in three carries energy) is put back explicitly.
func (u *upsampler) Frame(in []float32) []float32 {
	stuffed := make([]float32, len(in)*ratio)
	for i, s := range in {
		stuffed[i*ratio] = s * ratio
	}
	return u.filter.apply(stuffed)
}

// downsampler turns 48 kHz samples into the 16 kHz frames meowcaller wants.
type downsampler struct{ filter *lowPass }

func newDownsampler() *downsampler { return &downsampler{filter: newLowPass(mxRate)} }

// Frame returns len(in)/ratio samples at 16 kHz; len(in) must be a multiple of ratio.
//
// Filtered first, then decimated: decimating first is what folds everything above 8 kHz back into
// the audible band, and no amount of filtering afterwards can separate it again.
func (d *downsampler) Frame(in []float32) []float32 {
	filtered := d.filter.apply(in)
	out := make([]float32, len(in)/ratio)
	for i := range out {
		out[i] = filtered[i*ratio]
	}
	return out
}

// framer re-cuts a stream into fixed-size frames, because the two ends disagree about frame length
// as well as rate: three 20 ms Matrix frames make one 60 ms WhatsApp frame.
type framer struct {
	size int
	buf  []float32
}

func newFramer(size int) *framer { return &framer{size: size} }

// Push adds samples and returns whatever whole frames that completes.
func (f *framer) Push(in []float32) [][]float32 {
	f.buf = append(f.buf, in...)
	var out [][]float32
	for len(f.buf) >= f.size {
		frame := make([]float32, f.size)
		copy(frame, f.buf[:f.size])
		out = append(out, frame)
		f.buf = f.buf[f.size:]
	}
	return out
}

// Pending reports how many samples are held back waiting for a frame to fill.
func (f *framer) Pending() int { return len(f.buf) }
