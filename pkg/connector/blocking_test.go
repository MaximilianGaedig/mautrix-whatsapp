package connector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

var (
	blockLID  = types.NewJID("100000000000001", types.HiddenUserServer)
	blockPN   = types.NewJID("15550100", types.DefaultUserServer)
	blockLID2 = types.NewJID("100000000000002", types.HiddenUserServer)
)

func testAlt(jid types.JID) types.JID {
	switch jid {
	case blockLID:
		return blockPN
	case blockPN:
		return blockLID
	}
	return types.EmptyJID
}

func TestBlocklistChangeUpdates(t *testing.T) {
	evt := &events.Blocklist{Changes: []events.BlocklistChange{
		{JID: blockLID, Action: events.BlocklistChangeActionBlock},
		{JID: blockLID2, Action: events.BlocklistChangeActionUnblock},
		{JID: blockLID2, Action: "bogus"},
	}}
	updates, full := blocklistUpdates(evt, testAlt)
	assert.False(t, full)
	assert.Equal(t, []blockUpdate{
		{GhostID: "lid-100000000000001", Blocked: true},
		{GhostID: "15550100", Blocked: true, Alt: true},
		{GhostID: "lid-100000000000002", Blocked: false},
	}, updates)
}

func TestBlocklistModifyNeedsFullList(t *testing.T) {
	updates, full := blocklistUpdates(&events.Blocklist{Action: events.BlocklistActionModify}, testAlt)
	assert.True(t, full)
	assert.Empty(t, updates)
}

func TestBlocklistSnapshotUpdates(t *testing.T) {
	prev := map[networkid.UserID]struct{}{"lid-100000000000002": {}}
	updates := blocklistSnapshotUpdates(prev, []types.JID{blockLID}, testAlt)
	assert.ElementsMatch(t, []blockUpdate{
		{GhostID: "lid-100000000000001", Blocked: true},
		{GhostID: "15550100", Blocked: true, Alt: true},
		{GhostID: "lid-100000000000002", Blocked: false},
	}, updates)
}
