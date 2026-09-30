package connector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

func TestAboutExtraProfile(t *testing.T) {
	profile := aboutExtraProfile("Busy at the gym")
	// MSC4440: the biography field, in extensible events' m.text form.
	assert.Equal(t, "gay.fomx.biography", aboutProfileKey)
	assert.JSONEq(t, `{"m.text":[{"body":"Busy at the gym"}]}`, string(profile[aboutProfileKey]))

	// The way bridgev2 merges it into a ghost.
	var ghostProfile database.ExtraProfile
	assert.True(t, profile.CopyTo(&ghostProfile))
	assert.False(t, profile.CopyTo(&ghostProfile), "the same text again changes nothing")

	cleared := aboutExtraProfile("")
	assert.True(t, cleared.CopyTo(&ghostProfile), "an emptied about text is a change")
	assert.JSONEq(t, `null`, string(ghostProfile[aboutProfileKey]), "no about text clears the field")
}

func TestAboutFetchDue(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	assert.True(t, aboutFetchDue(time.Time{}, now), "never fetched")
	assert.False(t, aboutFetchDue(now.Add(-time.Hour), now))
	assert.False(t, aboutFetchDue(now.Add(-23*time.Hour), now))
	assert.True(t, aboutFetchDue(now.Add(-25*time.Hour), now))
}

func TestAboutNeedsFetch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ghost := func(id string, fetched time.Time) *bridgev2.Ghost {
		return &bridgev2.Ghost{Ghost: &database.Ghost{
			ID:       "15550100",
			Metadata: &waid.GhostMetadata{AboutFetched: jsontime.U(fetched)},
		}}
	}
	g := ghost("15550100", time.Time{})
	assert.True(t, aboutNeedsFetch(g, now))
	g.ID = "lid-100000000000001"
	assert.True(t, aboutNeedsFetch(g, now))
	g.ID = "bot-15550199"
	assert.False(t, aboutNeedsFetch(g, now), "bots have no about text")
	g = ghost("15550100", now.Add(-time.Hour))
	assert.False(t, aboutNeedsFetch(g, now), "fetched an hour ago")
}

func TestAboutUserInfoUpdatesGhost(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ghost := &bridgev2.Ghost{Ghost: &database.Ghost{ID: "15550100", Metadata: &waid.GhostMetadata{}}}
	info := aboutUserInfo("Hello there", now)
	assert.JSONEq(t, `{"m.text":[{"body":"Hello there"}]}`, string(info.ExtraProfile[aboutProfileKey]))
	assert.True(t, info.ExtraUpdates(context.Background(), ghost))
	assert.False(t, aboutNeedsFetch(ghost, now.Add(time.Hour)), "an event counts as a fetch")
	assert.True(t, aboutNeedsFetch(ghost, now.Add(25*time.Hour)))
}
