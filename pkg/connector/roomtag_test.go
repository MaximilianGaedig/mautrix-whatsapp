package connector

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/types"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func tagMsg(prev []event.RoomTag, now ...event.RoomTag) *bridgev2.MatrixRoomTag {
	mk := func(tags []event.RoomTag) *event.TagEventContent {
		content := &event.TagEventContent{Tags: event.Tags{}}
		for _, tag := range tags {
			content.Tags[tag] = event.TagMetadata{}
		}
		return content
	}
	msg := &bridgev2.MatrixRoomTag{}
	msg.Content = mk(now)
	msg.PrevContent = mk(prev)
	return msg
}

func indexes(patches []appstate.PatchInfo) [][]string {
	var out [][]string
	for _, patch := range patches {
		out = append(out, patch.Mutations[0].Index)
	}
	return out
}

func TestRoomTagPatches(t *testing.T) {
	chat := types.NewJID("15550100", types.DefaultUserServer)
	lastTS := time.Unix(1_700_000_000, 0)
	key := &waCommon.MessageKey{RemoteJID: new(string), ID: new(string)}
	const fav, archive = event.RoomTagFavourite, event.RoomTagLowPriority
	pinIdx := []string{appstate.IndexPin, chat.String()}
	archiveIdx := []string{appstate.IndexArchive, chat.String()}

	cases := []struct {
		name string
		msg  *bridgev2.MatrixRoomTag
		want [][]string
	}{
		{"pin", tagMsg(nil, fav), [][]string{pinIdx}},
		{"unpin", tagMsg([]event.RoomTag{fav}), [][]string{pinIdx}},
		{"archive", tagMsg(nil, archive), [][]string{archiveIdx}},
		{"unarchive", tagMsg([]event.RoomTag{archive}), [][]string{archiveIdx}},
		{"both", tagMsg(nil, fav, archive), [][]string{pinIdx, archiveIdx}},
		{"unchanged pin only touches archive", tagMsg([]event.RoomTag{fav}, fav, archive), [][]string{archiveIdx}},
		{"nothing changed", tagMsg([]event.RoomTag{fav}, fav), nil},
		{"unrelated tag", tagMsg(nil, "u.custom"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes := diffRoomTags(tc.msg, fav, archive)
			assert.Equal(t, tc.want, indexes(roomTagPatches(chat, changes, lastTS, key)))
		})
	}

	t.Run("archive values", func(t *testing.T) {
		changes := diffRoomTags(tagMsg(nil, archive), fav, archive)
		require.NotNil(t, changes.Archive)
		assert.True(t, *changes.Archive)
		assert.Nil(t, changes.Pin)
	})
	t.Run("archive tag not configured", func(t *testing.T) {
		changes := diffRoomTags(tagMsg(nil, archive), fav, "")
		assert.False(t, changes.any())
	})
}
