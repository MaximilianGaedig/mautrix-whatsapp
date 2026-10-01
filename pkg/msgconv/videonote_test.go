package msgconv

import (
	"encoding/json"
	"testing"

	"maunium.net/go/mautrix/event"
)

// roundVideo is a round video message as the Telegram bridge puts it into a Matrix room.
func roundVideo(t *testing.T, durationMS int) *event.MessageEventContent {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"msgtype": "m.video",
		"body":    "video.mp4",
		"info": map[string]any{
			"mimetype": "video/mp4", "w": 384, "h": 384, "duration": durationMS,
			"fi.mau.telegram.round_message": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var content event.MessageEventContent
	if err = json.Unmarshal(raw, &content); err != nil {
		t.Fatal(err)
	}
	return &content
}

func TestRoundVideoBecomesAVideoNote(t *testing.T) {
	msg := construct(roundVideo(t, 12000), "video/mp4")
	note := msg.GetPtvMessage()
	if msg.VideoMessage != nil || note.GetDirectPath() != "/x" || note.GetSeconds() != 12 || note.GetWidth() != 384 {
		t.Fatalf("round video is not a video note: %+v", msg)
	}
	if note.Caption != nil || note.GifPlayback != nil {
		t.Fatalf("video note carries fields of an ordinary video: %+v", note)
	}
}

func TestOrdinaryVideoIsNotAVideoNote(t *testing.T) {
	msg := construct(mediaContent(event.MsgVideo, "video/mp4"), "video/mp4")
	if msg.PtvMessage != nil || msg.VideoMessage == nil {
		t.Fatalf("ordinary video changed: %+v", msg)
	}
}

// A video note has no caption, lasts at most a minute and cannot be view-once, so a round video asking for
// any of those is sent as an ordinary video, which keeps everything but the shape.
func TestRoundVideoThatCannotBeANoteStaysAVideo(t *testing.T) {
	captioned := roundVideo(t, 12000)
	captioned.Body, captioned.FileName = "watch", "video.mp4"
	msg := construct(captioned, "video/mp4")
	if msg.PtvMessage != nil || msg.GetVideoMessage().GetCaption() != "watch" {
		t.Fatalf("captioned round video: %+v", msg)
	}

	msg = construct(roundVideo(t, 61000), "video/mp4")
	if msg.PtvMessage != nil || msg.GetVideoMessage().GetSeconds() != 61 {
		t.Fatalf("long round video: %+v", msg)
	}

	viewOnce := roundVideo(t, 12000)
	viewOnce.BeeperViewLimited = once()
	msg = construct(viewOnce, "video/mp4")
	if msg.PtvMessage != nil || !msg.GetViewOnceMessageV2().GetMessage().GetVideoMessage().GetViewOnce() {
		t.Fatalf("view-once round video: %+v", msg)
	}
}
