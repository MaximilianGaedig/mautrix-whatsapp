// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2026 Tulir Asokan
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

package msgconv

import (
	"time"

	"maunium.net/go/mautrix/event"
)

// RoundVideoInfoField is the key in the info of an m.video event that marks a round video message, which
// WhatsApp calls a video note. The Telegram bridge sets it on the round videos it bridges, and it is the only
// such marker in use on Matrix, so it is read here rather than adding a second one for the same thing.
const RoundVideoInfoField = "fi.mau.telegram.round_message"

// The WhatsApp apps record video notes of at most a minute.
const maxVideoNoteDuration = time.Minute

// isVideoNote tells whether a Matrix video should be sent as a WhatsApp video note. A video note has no
// caption, is short and cannot be view-once; a round video asking for any of those is sent as an ordinary
// video instead, which loses only the shape.
func isVideoNote(content *event.MessageEventContent, caption string) bool {
	info := content.GetInfo()
	round, _ := info.Extra[RoundVideoInfoField].(bool)
	return round && caption == "" && content.BeeperViewLimited == nil &&
		time.Duration(info.Duration)*time.Millisecond <= maxVideoNoteDuration
}
