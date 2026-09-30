// mautrix-whatsapp - A Matrix-WhatsApp puppeting bridge.
// Copyright (C) 2024 Tulir Asokan
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
	"bytes"
	"context"
	"fmt"
	"html"
	"image"
	"math"
	"net/http"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func (mc *MessageConverter) convertLocationMessage(ctx context.Context, msg *waE2E.LocationMessage) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	url := msg.GetURL()
	if len(url) == 0 {
		url = fmt.Sprintf("https://maps.google.com/?q=%.5f,%.5f", msg.GetDegreesLatitude(), msg.GetDegreesLongitude())
	}
	name := msg.GetName()
	if len(name) == 0 {
		latChar := 'N'
		if msg.GetDegreesLatitude() < 0 {
			latChar = 'S'
		}
		longChar := 'E'
		if msg.GetDegreesLongitude() < 0 {
			longChar = 'W'
		}
		name = fmt.Sprintf("%.4f° %c %.4f° %c", math.Abs(msg.GetDegreesLatitude()), latChar, math.Abs(msg.GetDegreesLongitude()), longChar)
	}

	content := &event.MessageEventContent{
		MsgType:       event.MsgLocation,
		Body:          fmt.Sprintf("Location: %s\n%s\n%s", name, msg.GetAddress(), url),
		Format:        event.FormatHTML,
		FormattedBody: fmt.Sprintf(`Location: <a href="%s">%s</a><br>%s`, html.EscapeString(url), html.EscapeString(name), html.EscapeString(msg.GetAddress())),
		GeoURI:        fmt.Sprintf("geo:%.5f,%.5f", msg.GetDegreesLatitude(), msg.GetDegreesLongitude()),
	}

	if len(msg.GetJPEGThumbnail()) > 0 {
		thumbnailMime := http.DetectContentType(msg.GetJPEGThumbnail())
		thumbnailURL, thumbnailFile, err := getIntent(ctx).UploadMedia(ctx, getPortal(ctx).MXID, msg.GetJPEGThumbnail(), "thumb.jpeg", thumbnailMime)
		if err == nil {
			cfg, _, _ := image.DecodeConfig(bytes.NewReader(msg.GetJPEGThumbnail()))
			content.Info = &event.FileInfo{
				ThumbnailInfo: &event.FileInfo{
					Size:     len(msg.GetJPEGThumbnail()),
					Width:    cfg.Width,
					Height:   cfg.Height,
					MimeType: thumbnailMime,
				},
				ThumbnailURL:  thumbnailURL,
				ThumbnailFile: thumbnailFile,
			}
		}
	}

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
	}, msg.GetContextInfo()
}

func (mc *MessageConverter) convertLiveLocationMessage(ctx context.Context, msg *waE2E.LiveLocationMessage) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	// Linked devices, which the bridge is one of, only ever get where a live location started: WhatsApp sends
	// the updates to phones alone. So it's the starting point, as a location, and not a Matrix live location
	// that would sit on that point as if it were still live.
	part, contextInfo := mc.convertLocationMessage(ctx, &waE2E.LocationMessage{
		DegreesLatitude:  msg.DegreesLatitude,
		DegreesLongitude: msg.DegreesLongitude,
		Name:             ptr.Ptr("Live location"),
		JPEGThumbnail:    msg.JPEGThumbnail,
		ContextInfo:      msg.ContextInfo,
	})
	part.Content.Body += "\nWhatsApp only shows where it goes next on the phone."
	part.Content.FormattedBody += "<br>WhatsApp only shows where it goes next on the phone."
	if caption := msg.GetCaption(); caption != "" {
		part.Content.Body = caption + "\n" + part.Content.Body
		part.Content.FormattedBody = html.EscapeString(caption) + "<br>" + part.Content.FormattedBody
	}
	return part, contextInfo
}
