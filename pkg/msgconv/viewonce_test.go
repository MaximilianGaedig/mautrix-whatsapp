package msgconv

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func uploaded() *whatsmeow.UploadResponse {
	return &whatsmeow.UploadResponse{URL: "https://mmg.whatsapp.net/x", DirectPath: "/x", FileLength: 3}
}

func once() *event.BeeperViewLimitedMedia {
	return &event.BeeperViewLimitedMedia{Type: "count", Count: 1}
}

func mediaContent(msgType event.MessageType, mime string) *event.MessageEventContent {
	return &event.MessageEventContent{MsgType: msgType, Body: "file", Info: &event.FileInfo{MimeType: mime}}
}

func construct(content *event.MessageEventContent, mime string) *waE2E.Message {
	return (&MessageConverter{}).constructMediaMessage(
		context.Background(), content, &event.Event{Type: event.EventMessage}, uploaded(), nil, &waE2E.ContextInfo{}, mime,
	)
}

func TestViewOncePhotoAndVideoAreWrapped(t *testing.T) {
	photo := mediaContent(event.MsgImage, "image/jpeg")
	photo.Body, photo.FileName = "look at this", "a.jpg"
	photo.BeeperViewLimited = once()
	msg := construct(photo, "image/jpeg")
	inner := msg.GetViewOnceMessageV2().GetMessage().GetImageMessage()
	if msg.ImageMessage != nil || !inner.GetViewOnce() || inner.GetDirectPath() != "/x" {
		t.Fatalf("photo is not a view-once message: %+v", msg)
	}
	// WhatsApp, unlike Signal, shows a caption on view-once media.
	if inner.GetCaption() != "look at this" {
		t.Fatalf("caption lost: %q", inner.GetCaption())
	}

	video := mediaContent(event.MsgVideo, "video/mp4")
	video.BeeperViewLimited = once()
	msg = construct(video, "video/mp4")
	if msg.VideoMessage != nil || !msg.GetViewOnceMessageV2().GetMessage().GetVideoMessage().GetViewOnce() {
		t.Fatalf("video is not a view-once message: %+v", msg)
	}
}

func TestViewOnceVoiceMessageUsesTheExtensionWrapper(t *testing.T) {
	voice := mediaContent(event.MsgAudio, "audio/ogg; codecs=opus")
	voice.MSC3245Voice = &event.MSC3245Voice{}
	voice.BeeperViewLimited = once()
	msg := construct(voice, "audio/ogg; codecs=opus")
	inner := msg.GetViewOnceMessageV2Extension().GetMessage().GetAudioMessage()
	if msg.AudioMessage != nil || msg.ViewOnceMessageV2 != nil || !inner.GetViewOnce() || !inner.GetPTT() {
		t.Fatalf("voice message is not a view-once message: %+v", msg)
	}
}

func TestOrdinaryMediaIsNotWrapped(t *testing.T) {
	msg := construct(mediaContent(event.MsgImage, "image/jpeg"), "image/jpeg")
	if msg.ImageMessage == nil || msg.ImageMessage.ViewOnce != nil || msg.ViewOnceMessageV2 != nil {
		t.Fatalf("ordinary photo changed: %+v", msg)
	}
}

// This goes last: without the check the converter goes on to download the media, which a bare converter
// cannot do.
func TestViewOnceIsRefusedBeforeUploading(t *testing.T) {
	music := mediaContent(event.MsgAudio, "audio/mpeg")
	gif := mediaContent(event.MsgVideo, "video/mp4")
	gif.Info.MauGIF = true
	twice := mediaContent(event.MsgImage, "image/jpeg")
	for _, tc := range []struct {
		name     string
		content  *event.MessageEventContent
		limit    *event.BeeperViewLimitedMedia
		disabled bool
	}{
		{"file", mediaContent(event.MsgFile, "application/pdf"), once(), false},
		{"music", music, once(), false},
		{"gif", gif, once(), false},
		{"two views", twice, &event.BeeperViewLimitedMedia{Type: "count", Count: 2}, false},
		{"disable_view_once", mediaContent(event.MsgImage, "image/jpeg"), once(), true},
	} {
		tc.content.BeeperViewLimited = tc.limit
		mc := &MessageConverter{DisableViewOnce: tc.disabled}
		portal := &bridgev2.Portal{Portal: &database.Portal{Metadata: &waid.PortalMetadata{}}}
		evt := &event.Event{Type: event.EventMessage, Content: event.Content{Parsed: tc.content}}
		_, _, err := mc.ToWhatsApp(context.Background(), nil, evt, tc.content, nil, nil, portal)
		if !errors.Is(err, bridgev2.ErrUnsupportedViewLimitedType) {
			t.Errorf("%s: got %v, want %v", tc.name, err, bridgev2.ErrUnsupportedViewLimitedType)
		}
	}
}
