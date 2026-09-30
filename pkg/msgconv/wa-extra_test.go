package msgconv

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mau.fi/util/ptr"
	"go.mau.fi/whatsmeow/proto/waE2E"
)

func TestExtraMessageTypes(t *testing.T) {
	mc := &MessageConverter{}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"status mention", &waE2E.Message{StatusMentionMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{Conversation: ptr.Ptr("look")},
		}}, "Mentioned you in their status: look"},
		{"sticker pack", &waE2E.Message{StickerPackMessage: &waE2E.StickerPackMessage{
			Name: ptr.Ptr("Cats"), Publisher: ptr.Ptr("Someone"), Stickers: make([]*waE2E.StickerPackMessage_Sticker, 3),
		}}, `Sticker pack "Cats" by Someone (3 stickers)`},
		{"payment request", &waE2E.Message{RequestPaymentMessage: &waE2E.RequestPaymentMessage{
			Amount: &waE2E.Money{Value: ptr.Ptr(int64(12345)), Offset: ptr.Ptr(uint32(2)), CurrencyCode: ptr.Ptr("EUR")},
		}}, "Requested a payment of 123.45 EUR"},
		{"legacy payment request", &waE2E.Message{RequestPaymentMessage: &waE2E.RequestPaymentMessage{
			Amount1000: ptr.Ptr(uint64(5000)), CurrencyCodeIso4217: ptr.Ptr("INR"),
		}}, "Requested a payment of 5.00 INR"},
		{"sent payment", &waE2E.Message{SendPaymentMessage: &waE2E.SendPaymentMessage{
			NoteMessage: &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: ptr.Ptr("dinner")}},
		}}, "Sent a payment: dinner"},
		{"scheduled call", &waE2E.Message{ScheduledCallCreationMessage: &waE2E.ScheduledCallCreationMessage{
			Title: ptr.Ptr("Standup"), CallType: waE2E.ScheduledCallCreationMessage_VIDEO.Enum(),
			ScheduledTimestampMS: ptr.Ptr(time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC).UnixMilli()),
		}}, `Scheduled a video call "Standup" for Thu 1 Oct 2026 09:30 UTC`},
		{"cancelled call", &waE2E.Message{ScheduledCallEditMessage: &waE2E.ScheduledCallEditMessage{
			EditType: waE2E.ScheduledCallEditMessage_CANCEL.Enum(),
		}}, "Cancelled a scheduled call"},
		{"poll results", &waE2E.Message{PollResultSnapshotMessage: &waE2E.PollResultSnapshotMessage{
			Name: ptr.Ptr("Lunch?"),
			PollVotes: []*waE2E.PollResultSnapshotMessage_PollVote{
				{OptionName: ptr.Ptr("Yes"), OptionVoteCount: ptr.Ptr(int64(2))},
				{OptionName: ptr.Ptr("No"), OptionVoteCount: ptr.Ptr(int64(1))},
			},
		}}, "Poll results: Lunch?\n• Yes: 2\n• No: 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			switch {
			case tc.msg.StatusMentionMessage != nil:
				p, _ := mc.convertStatusMentionMessage(ctx, tc.msg.StatusMentionMessage)
				got = p.Content.Body
			case tc.msg.StickerPackMessage != nil:
				p, _ := mc.convertStickerPackMessage(ctx, tc.msg.StickerPackMessage)
				got = p.Content.Body
			case tc.msg.ScheduledCallCreationMessage != nil, tc.msg.ScheduledCallEditMessage != nil:
				p, _ := mc.convertScheduledCallMessage(ctx, tc.msg)
				got = p.Content.Body
			case tc.msg.PollResultSnapshotMessage != nil:
				p, _ := mc.convertPollResultSnapshotMessage(ctx, tc.msg.PollResultSnapshotMessage)
				got = p.Content.Body
			default:
				p, _ := mc.convertPaymentMessage(ctx, tc.msg)
				got = p.Content.Body
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestJoinVCards(t *testing.T) {
	data, names := joinVCards([]*waE2E.ContactMessage{
		{DisplayName: ptr.Ptr("A"), Vcard: ptr.Ptr("BEGIN:VCARD\r\nFN:A\r\nEND:VCARD\r\n")},
		{DisplayName: ptr.Ptr("No card")},
		{DisplayName: ptr.Ptr("B"), Vcard: ptr.Ptr("BEGIN:VCARD\r\nFN:B\r\nEND:VCARD")},
	})
	if strings.Count(string(data), "BEGIN:VCARD") != 2 || len(names) != 2 || names[1] != "B" {
		t.Errorf("data %q, names %v", data, names)
	}
	if !strings.Contains(string(data), "END:VCARD\r\nBEGIN:VCARD") {
		t.Errorf("cards not separated by a line break: %q", data)
	}
	if data, _ = joinVCards([]*waE2E.ContactMessage{{DisplayName: ptr.Ptr("x")}}); data != nil {
		t.Error("no vCards, no file")
	}
}
