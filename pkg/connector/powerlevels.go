package connector

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

var _ bridgev2.PowerLevelHandlingNetworkAPI = (*WhatsAppClient)(nil)

// crossedAdmin reports whether a power level change makes someone a group admin (+1), stops them being one
// (-1), or neither (0). Admin is adminPL and up, as the chat info maps it.
func crossedAdmin(change *bridgev2.SinglePowerLevelChange) int {
	if change == nil {
		return 0
	}
	wasAdmin, isAdmin := change.OrigLevel >= adminPL, change.NewLevel >= adminPL
	switch {
	case isAdmin && !wasAdmin:
		return 1
	case wasAdmin && !isAdmin:
		return -1
	}
	return 0
}

func (wa *WhatsAppClient) targetJID(ctx context.Context, target bridgev2.GhostOrUserLogin) (types.JID, error) {
	switch target := target.(type) {
	case *bridgev2.Ghost:
		return waid.ParseUserID(target.ID), nil
	case *bridgev2.UserLogin:
		ghost, err := target.Bridge.GetGhostByID(ctx, networkid.UserID(target.ID))
		if err != nil {
			return types.EmptyJID, err
		}
		return waid.ParseUserID(ghost.ID), nil
	}
	return types.EmptyJID, fmt.Errorf("unknown target %T", target)
}

// HandleMatrixPowerLevels makes people group admins or not, and changes who may send messages
// (announcement groups) and who may edit the group's info (locked groups), from the room's power levels.
func (wa *WhatsAppClient) HandleMatrixPowerLevels(ctx context.Context, msg *bridgev2.MatrixPowerLevelChange) (bool, error) {
	if msg.Portal.RoomType == database.RoomTypeDM {
		return false, nil
	}
	groupJID, err := waid.ParsePortalID(msg.Portal.ID)
	if err != nil {
		return false, err
	}
	var promote, demote []types.JID
	for _, change := range msg.Users {
		direction := crossedAdmin(&change.SinglePowerLevelChange)
		if direction == 0 {
			continue
		}
		jid, err := wa.targetJID(ctx, change.Target)
		if err != nil {
			return false, err
		}
		if direction > 0 {
			promote = append(promote, jid)
		} else {
			demote = append(demote, jid)
		}
	}
	changed := false
	for action, jids := range map[whatsmeow.ParticipantChange][]types.JID{
		whatsmeow.ParticipantChangePromote: promote,
		whatsmeow.ParticipantChangeDemote:  demote,
	} {
		if len(jids) == 0 {
			continue
		}
		if _, err = wa.Client.UpdateGroupParticipants(ctx, groupJID, jids, action); err != nil {
			return changed, fmt.Errorf("failed to %s group admins: %w", action, err)
		}
		changed = true
	}
	if direction := crossedAdmin(msg.EventsDefault); direction != 0 {
		if err = wa.Client.SetGroupAnnounce(ctx, groupJID, direction > 0); err != nil {
			return changed, fmt.Errorf("failed to change who can send messages: %w", err)
		}
		changed = true
	}
	if direction := crossedAdmin(msg.Events[event.StateRoomName.Type]); direction != 0 {
		if err = wa.Client.SetGroupLocked(ctx, groupJID, direction > 0); err != nil {
			return changed, fmt.Errorf("failed to change who can edit group info: %w", err)
		}
		changed = true
	}
	return changed, nil
}
