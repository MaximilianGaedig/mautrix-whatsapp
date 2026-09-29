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
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/pion/rtp"
	opus "gopkg.in/hraban/opus.v2"
)

/*
 * Opus, because a WhatsApp call arrives as samples and Matrix wants packets.
 *
 * Signal and Messenger both speak Opus on the wire, so those bridges relay RTP without ever looking
 * at a payload. WhatsApp does not: meowcaller hands over decoded 16 kHz PCM, because the call is
 * carried in Meta's MLow codec, which nothing on the Matrix side can play. So this is the one bridge
 * that has to be a transcoder, and that means an Opus *encoder*.
 *
 * libopus is used rather than a Go implementation because there is no Go Opus encoder -
 * github.com/pion/opus, already in the tree, decodes only. The alternative was to offer G.711 to the
 * Matrix leg, which is mandatory in WebRTC and trivial to write, but it is 8 kHz narrowband: every
 * WhatsApp call would arrive audibly worse than the same call in the app, to save a dependency the
 * image is already set up for (it links olm, so cgo is on and the builder has build-base).
 *
 * The RTP clock is 48 kHz regardless of the samples' own rate, per RFC 7587: one 20 ms frame
 * advances the timestamp by 960 whatever Opus was fed.
 */

const (
	// opusFrame is 20 ms at 48 kHz, the frame WebRTC endpoints expect.
	opusFrame = mxFrame
	// opusMaxPacket is comfortably above a 20 ms mono VoIP frame; libopus needs somewhere to write
	// before it knows how small the frame compressed to.
	opusMaxPacket = 1276
	// opusBitrate is what WhatsApp's own audio is worth: the source is 16 kHz mono, so spending more
	// than this buys nothing but bandwidth.
	opusBitrate = 24000
)

// opusEncoder turns 48 kHz mono PCM into Opus frames.
type opusEncoder struct {
	enc *opus.Encoder
	buf []byte
}

func newOpusEncoder() (*opusEncoder, error) {
	// AppVoIP rather than AppAudio: it favours speech intelligibility over musical fidelity, which
	// is what is coming out of a phone call.
	enc, err := opus.NewEncoder(mxRate, 1, opus.AppVoIP)
	if err != nil {
		return nil, fmt.Errorf("creating opus encoder: %w", err)
	}
	if err = enc.SetBitrate(opusBitrate); err != nil {
		return nil, fmt.Errorf("setting opus bitrate: %w", err)
	}
	// The other end is a phone network with real loss; in-band FEC lets the decoder rebuild a lost
	// frame from the next one instead of playing a gap.
	if err = enc.SetInBandFEC(true); err != nil {
		return nil, fmt.Errorf("enabling opus FEC: %w", err)
	}
	// DTX stops sending during silence, which is most of a conversation.
	if err = enc.SetDTX(true); err != nil {
		return nil, fmt.Errorf("enabling opus DTX: %w", err)
	}
	return &opusEncoder{enc: enc, buf: make([]byte, opusMaxPacket)}, nil
}

// Encode compresses exactly one 20 ms frame.
//
// The returned slice aliases an internal buffer and is only valid until the next call, which is
// fine for the one caller here: it hands it straight to WriteRTP, which copies.
func (e *opusEncoder) Encode(pcm []float32) ([]byte, error) {
	if len(pcm) != opusFrame {
		return nil, fmt.Errorf("opus wants %d samples per frame, got %d", opusFrame, len(pcm))
	}
	n, err := e.enc.EncodeFloat32(pcm, e.buf)
	if err != nil {
		return nil, fmt.Errorf("encoding opus: %w", err)
	}
	return e.buf[:n], nil
}

// opusDecoder turns Opus frames back into 48 kHz mono PCM.
type opusDecoder struct {
	dec *opus.Decoder
	pcm []float32
}

func newOpusDecoder() (*opusDecoder, error) {
	dec, err := opus.NewDecoder(mxRate, 1)
	if err != nil {
		return nil, fmt.Errorf("creating opus decoder: %w", err)
	}
	// Room for the longest frame Opus permits (120 ms), not just the 20 ms we expect: the remote
	// picks its own frame length and a wrong guess here is a buffer overrun, not a glitch.
	return &opusDecoder{dec: dec, pcm: make([]float32, mxRate/1000*120)}, nil
}

// Decode expands one Opus packet. The returned slice aliases an internal buffer.
func (d *opusDecoder) Decode(payload []byte) ([]float32, error) {
	if len(payload) == 0 {
		// A zero-length payload is DTX: the sender has stopped transmitting because nobody is
		// talking. Conceal it rather than passing an empty frame on, so the timeline stays intact.
		return d.Conceal()
	}
	n, err := d.dec.DecodeFloat32(payload, d.pcm)
	if err != nil {
		return nil, fmt.Errorf("decoding opus: %w", err)
	}
	return d.pcm[:n], nil
}

// Conceal produces one frame's worth of loss concealment, for a packet that never arrived.
//
// Handing the decoder a gap matters beyond the missing audio: its internal state is predictive, so
// skipping a frame outright makes the *next* real frame decode wrong too.
func (d *opusDecoder) Conceal() ([]float32, error) {
	// DecodePLCFloat32 rather than decoding a nil packet: libopus infers the concealed frame's
	// length from the buffer it is given, so the buffer is sized to the frame we want back.
	if err := d.dec.DecodePLCFloat32(d.pcm[:opusFrame]); err != nil {
		return nil, fmt.Errorf("opus loss concealment: %w", err)
	}
	return d.pcm[:opusFrame], nil
}

/*
 * Packetising.
 *
 * Relaying bridges forward someone else's packets and inherit their numbering. This one originates
 * the stream, so it has to number it: a random starting point (RFC 3550 asks for one, so that a
 * stream cannot be trivially predicted or spoofed), then strictly monotonic from there. SSRC and
 * payload type are deliberately left alone - TrackLocalStaticRTP stamps both with what its own
 * PeerConnection negotiated, which is not knowable here.
 */
type opusPacketiser struct {
	sequence  uint16
	timestamp uint32
	started   bool
}

func newOpusPacketiser() *opusPacketiser {
	var seed [6]byte
	// A failure here would mean the system entropy source is broken, which is not a condition this
	// can sensibly continue through - but it is also not worth an error return, so fall back to a
	// fixed start rather than panicking in the middle of a call.
	_, _ = rand.Read(seed[:])
	return &opusPacketiser{
		sequence:  binary.BigEndian.Uint16(seed[0:2]),
		timestamp: binary.BigEndian.Uint32(seed[2:6]),
	}
}

// Next wraps one encoded frame in an RTP packet.
func (p *opusPacketiser) Next(payload []byte) *rtp.Packet {
	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			SequenceNumber: p.sequence,
			Timestamp:      p.timestamp,
			// The marker bit means "first packet of a talkspurt". Only the very first packet of the
			// stream is marked here; marking every frame would have the receiver reset its jitter
			// buffer fifty times a second.
			Marker: !p.started,
		},
		Payload: payload,
	}
	p.started = true
	p.sequence++
	// Advances by the frame duration whether or not the frame was sent, so a DTX gap leaves a hole
	// in the timestamps that tells the receiver how long the silence was.
	p.timestamp += opusFrame
	return packet
}
