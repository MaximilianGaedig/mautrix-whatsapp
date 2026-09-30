package connector

import (
	"time"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

// roomTagChanges is what a Matrix tag update changed about a chat: nil for what stayed the same.
type roomTagChanges struct {
	Pin     *bool
	Archive *bool
}

func (c roomTagChanges) any() bool { return c.Pin != nil || c.Archive != nil }

func (wa *WhatsAppClient) roomTagChanges(msg *bridgev2.MatrixRoomTag) roomTagChanges {
	return diffRoomTags(msg, wa.Main.Config.PinnedTag, wa.Main.Config.ArchiveTag)
}

func diffRoomTags(msg *bridgev2.MatrixRoomTag, pinnedTag, archiveTag event.RoomTag) roomTagChanges {
	changes := roomTagChanges{Pin: bridgev2.TagChange(msg, pinnedTag)}
	if archiveTag != pinnedTag {
		changes.Archive = bridgev2.TagChange(msg, archiveTag)
	}
	return changes
}

// roomTagPatches turns tag changes into the app state patches that make WhatsApp match.
func roomTagPatches(chat types.JID, changes roomTagChanges, lastTS time.Time, lastKey *waCommon.MessageKey) []appstate.PatchInfo {
	var patches []appstate.PatchInfo
	if changes.Pin != nil {
		patches = append(patches, appstate.BuildPin(chat, *changes.Pin))
	}
	if changes.Archive != nil {
		patches = append(patches, appstate.BuildArchive(chat, *changes.Archive, lastTS, lastKey))
	}
	return patches
}
