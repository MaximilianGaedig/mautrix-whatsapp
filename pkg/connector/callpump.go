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
	"sync"

	"github.com/pion/rtp"
)

/*
 * The two directions of a WhatsApp call, each as one object that can be tested without a call.
 *
 * Signal and Messenger relay RTP, so their direction objects are a copy loop. This one transcodes,
 * so each direction is a chain - resample, re-cut, code - and every link has a frame size that has
 * to line up with its neighbours. Getting one wrong does not fail: it produces audio that is fast,
 * slow, or chipmunked. Keeping the chains here, apart from the session, is what lets a test drive a
 * tone through them and check what comes out the other end.
 */

// waToMatrix carries WhatsApp's 16 kHz frames to the Matrix leg as Opus RTP.
type waToMatrix struct {
	up         *upsampler
	encoder    *opusEncoder
	packetiser *opusPacketiser
}

func newWAToMatrix() (*waToMatrix, error) {
	encoder, err := newOpusEncoder()
	if err != nil {
		return nil, err
	}
	return &waToMatrix{up: newUpsampler(), encoder: encoder, packetiser: newOpusPacketiser()}, nil
}

// Frame converts one 60 ms WhatsApp frame into the three 20 ms Opus packets it is worth.
//
// The count is exact by construction - 960 samples at 16 kHz upsample to 2880 at 48 kHz, which is
// three Opus frames - so there is no remainder to carry and no buffer to keep.
func (w *waToMatrix) Frame(pcm []float32) ([]*rtp.Packet, error) {
	wide := w.up.Frame(pcm)
	packets := make([]*rtp.Packet, 0, len(wide)/opusFrame)
	for offset := 0; offset+opusFrame <= len(wide); offset += opusFrame {
		payload, err := w.encoder.Encode(wide[offset : offset+opusFrame])
		if err != nil {
			return nil, err
		}
		packet := w.packetiser.Next(payload)
		if len(payload) == 0 {
			/*
			 * DTX: the encoder decided this frame is silence and produced nothing.
			 *
			 * Nothing is sent, but the packetiser was still advanced, so the timestamps of the
			 * frames around the gap stay true to the clock and the receiver plays the silence at
			 * its real length instead of closing it up.
			 */
			continue
		}
		// The payload aliases the encoder's buffer, which the next frame overwrites; the packet may
		// outlive this call, so it gets its own copy.
		packet.Payload = append([]byte(nil), payload...)
		packets = append(packets, packet)
	}
	return packets, nil
}

// matrixToWA carries Opus from the Matrix leg back to WhatsApp's 16 kHz frames.
type matrixToWA struct {
	decoder *opusDecoder
	down    *downsampler
	framer  *framer
}

func newMatrixToWA() (*matrixToWA, error) {
	decoder, err := newOpusDecoder()
	if err != nil {
		return nil, err
	}
	// The framer is needed here and not in the other direction because the ratio runs the other
	// way: three 20 ms Matrix packets make one 60 ms WhatsApp frame, so two out of three packets
	// produce nothing and the third produces one frame.
	return &matrixToWA{decoder: decoder, down: newDownsampler(), framer: newFramer(waFrame)}, nil
}

// Packet converts one Opus payload into however many whole WhatsApp frames it completes.
func (m *matrixToWA) Packet(payload []byte) ([][]float32, error) {
	pcm, err := m.decoder.Decode(payload)
	if err != nil {
		return nil, err
	}
	return m.framer.Push(m.down.Frame(pcm)), nil
}

// Conceal fills in for a packet that never arrived, so the decoder's predictive state stays sound
// and the gap is played as its real length rather than skipped over.
func (m *matrixToWA) Conceal() ([][]float32, error) {
	pcm, err := m.decoder.Conceal()
	if err != nil {
		return nil, err
	}
	return m.framer.Push(m.down.Frame(pcm)), nil
}

/*
 * pcmPipe is the AudioSource meowcaller plays from, fed by whatever arrives from Matrix.
 *
 * Two clocks meet here and neither is in charge: Matrix delivers when packets arrive off the
 * network, WhatsApp asks for a frame every 60 ms. A channel between them has to be bounded, and
 * when it fills the choice is to block or to drop.
 *
 * It drops, oldest first. Blocking would push back on the RTP reader, which cannot slow the network
 * down, so the backlog would simply move somewhere with no limit at all; and a call that falls
 * behind and stays behind is worse than one that skips - latency never comes back on its own, while
 * a dropped frame costs 60 ms once. depth is what that costs in the worst case.
 */
type pcmPipe struct {
	lock   sync.Mutex
	frames [][]float32
	ready  chan struct{}
	closed bool
	// Dropped counts frames thrown away, which is the signal that the far end is outrunning us.
	Dropped int
}

// pcmPipeDepth is a little under half a second of audio. Deep enough to ride out ordinary jitter,
// shallow enough that a listener never hears a reply to something said half a second ago.
const pcmPipeDepth = 8

func newPCMPipe() *pcmPipe {
	return &pcmPipe{ready: make(chan struct{}, 1)}
}

// Push adds a frame, dropping the oldest if the pipe is already full.
func (p *pcmPipe) Push(frame []float32) {
	p.lock.Lock()
	if p.closed {
		p.lock.Unlock()
		return
	}
	if len(p.frames) >= pcmPipeDepth {
		p.frames = p.frames[1:]
		p.Dropped++
	}
	p.frames = append(p.frames, frame)
	p.lock.Unlock()
	select {
	case p.ready <- struct{}{}:
	default:
	}
}

// ReadFrame satisfies meowcaller's AudioSource.
//
// It never blocks: a call whose far side has gone quiet still has to send something every 60 ms, so
// an empty pipe yields silence rather than stalling the sender's clock.
func (p *pcmPipe) ReadFrame() ([]float32, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.closed {
		return nil, io.EOF
	}
	if len(p.frames) == 0 {
		return make([]float32, waFrame), nil
	}
	frame := p.frames[0]
	p.frames = p.frames[1:]
	return frame, nil
}

// Close ends the source, which stops the Player.
func (p *pcmPipe) Close() error {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.closed = true
	p.frames = nil
	return nil
}

// Depth reports how many frames are waiting, for logging how far behind the call is running.
func (p *pcmPipe) Depth() int {
	p.lock.Lock()
	defer p.lock.Unlock()
	return len(p.frames)
}
