package msgconv

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// Message types WhatsApp's linked devices show as a line of text, which the bridge used to replace with
// "Unknown message type". Each becomes a notice saying what it is, with the details the message carries.

func noticePart(body string) *bridgev2.ConvertedMessagePart {
	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: &event.MessageEventContent{MsgType: event.MsgNotice, Body: body},
	}
}

func (mc *MessageConverter) convertStatusMentionMessage(ctx context.Context, msg *waE2E.FutureProofMessage) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	body := "Mentioned you in their status"
	inner := msg.GetMessage()
	if text := inner.GetConversation(); text != "" {
		body += ": " + text
	} else if text = inner.GetExtendedTextMessage().GetText(); text != "" {
		body += ": " + text
	}
	return noticePart(body), nil
}

func (mc *MessageConverter) convertStickerPackMessage(ctx context.Context, msg *waE2E.StickerPackMessage) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	body := fmt.Sprintf("Sticker pack %q", msg.GetName())
	if publisher := msg.GetPublisher(); publisher != "" {
		body += " by " + publisher
	}
	body += fmt.Sprintf(" (%d stickers)", len(msg.GetStickers()))
	if caption := msg.GetCaption(); caption != "" {
		body += "\n" + caption
	}
	return noticePart(body), msg.GetContextInfo()
}

// formatMoney renders WhatsApp's money: value / 10^offset in currency.
func formatMoney(value int64, offset uint32, currency string) string {
	amount := float64(value) / math.Pow10(int(offset))
	return strings.TrimSpace(fmt.Sprintf("%.2f %s", amount, currency))
}

func paymentNote(note *waE2E.Message) string {
	if text := note.GetExtendedTextMessage().GetText(); text != "" {
		return ": " + text
	}
	if text := note.GetConversation(); text != "" {
		return ": " + text
	}
	return ""
}

func (mc *MessageConverter) convertPaymentMessage(ctx context.Context, waMsg *waE2E.Message) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	switch {
	case waMsg.SendPaymentMessage != nil:
		return noticePart("Sent a payment" + paymentNote(waMsg.SendPaymentMessage.GetNoteMessage())), nil
	case waMsg.RequestPaymentMessage != nil:
		req := waMsg.RequestPaymentMessage
		amount := ""
		if money := req.GetAmount(); money != nil {
			amount = " of " + formatMoney(money.GetValue(), money.GetOffset(), money.GetCurrencyCode())
		} else if req.Amount1000 != nil {
			amount = " of " + formatMoney(int64(req.GetAmount1000()), 3, req.GetCurrencyCodeIso4217())
		}
		return noticePart("Requested a payment" + amount + paymentNote(req.GetNoteMessage())), nil
	case waMsg.DeclinePaymentRequestMessage != nil:
		return noticePart("Declined a payment request"), nil
	case waMsg.CancelPaymentRequestMessage != nil:
		return noticePart("Cancelled a payment request"), nil
	default:
		return noticePart("Invited you to send payments on WhatsApp"), nil
	}
}

func (mc *MessageConverter) convertScheduledCallMessage(ctx context.Context, waMsg *waE2E.Message) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	if edit := waMsg.ScheduledCallEditMessage; edit != nil {
		if edit.GetEditType() == waE2E.ScheduledCallEditMessage_CANCEL {
			return noticePart("Cancelled a scheduled call"), nil
		}
		return noticePart("Changed a scheduled call"), nil
	}
	call := waMsg.GetScheduledCallCreationMessage()
	kind := "call"
	switch call.GetCallType() {
	case waE2E.ScheduledCallCreationMessage_VOICE:
		kind = "voice call"
	case waE2E.ScheduledCallCreationMessage_VIDEO:
		kind = "video call"
	}
	body := "Scheduled a " + kind
	if title := call.GetTitle(); title != "" {
		body += fmt.Sprintf(" %q", title)
	}
	if ms := call.GetScheduledTimestampMS(); ms > 0 {
		body += " for " + time.UnixMilli(ms).UTC().Format("Mon 2 Jan 2006 15:04 MST")
	}
	return noticePart(body), nil
}

func (mc *MessageConverter) convertPollResultSnapshotMessage(ctx context.Context, msg *waE2E.PollResultSnapshotMessage) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Poll results: %s", msg.GetName())
	for _, vote := range msg.GetPollVotes() {
		fmt.Fprintf(&sb, "\n• %s: %d", vote.GetOptionName(), vote.GetOptionVoteCount())
	}
	return noticePart(sb.String()), msg.GetContextInfo()
}

func (mc *MessageConverter) convertRequestPhoneNumberMessage(ctx context.Context, msg *waE2E.RequestPhoneNumberMessage) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	return noticePart("Asked for your phone number. Share it from the WhatsApp app."), msg.GetContextInfo()
}

// convertContactsArrayMessage puts all the contacts in one vCard file, as a single contact is one: a .vcf
// file may hold any number of cards.
func (mc *MessageConverter) convertContactsArrayMessage(ctx context.Context, msg *waE2E.ContactsArrayMessage) (*bridgev2.ConvertedMessagePart, *waE2E.ContextInfo) {
	data, names := joinVCards(msg.GetContacts())
	if data == nil {
		return noticePart("Shared contacts, but none came with details"), msg.GetContextInfo()
	}
	fileName := fmt.Sprintf("%d contacts.vcf", len(names))
	if name := msg.GetDisplayName(); name != "" {
		fileName = name + ".vcf"
	}
	mxc, file, err := getIntent(ctx).UploadMedia(ctx, getPortal(ctx).MXID, data, fileName, "text/vcard")
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to reupload WhatsApp contact array")
		return noticePart("Shared contacts: " + strings.Join(names, ", ")), msg.GetContextInfo()
	}
	return &bridgev2.ConvertedMessagePart{
		Type: event.EventMessage,
		Content: &event.MessageEventContent{
			MsgType:  event.MsgFile,
			Body:     fileName,
			FileName: fileName,
			URL:      mxc,
			File:     file,
			Info:     &event.FileInfo{MimeType: "text/vcard", Size: len(data)},
		},
	}, msg.GetContextInfo()
}

// joinVCards is the contacts' vCards as one file, and their names; nil when none has a vCard.
func joinVCards(contacts []*waE2E.ContactMessage) (data []byte, names []string) {
	cards := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		if vcard := strings.TrimSpace(contact.GetVcard()); vcard != "" {
			cards = append(cards, vcard)
			names = append(names, contact.GetDisplayName())
		}
	}
	if len(cards) == 0 {
		return nil, nil
	}
	return []byte(strings.Join(cards, "\r\n") + "\r\n"), names
}
