package connector

import (
	"context"
	"time"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"go.mau.fi/mautrix-whatsapp/pkg/waid"
)

// aboutProfileKey is where a user's WhatsApp "about" text goes in the ghost's extra profile: the bio key every
// bridge shares, so Matrix clients show it the same for every network.
const aboutProfileKey = "im.mxg.bio"

// aboutRefetchInterval is how often a user's about text is fetched on its own. Changes in between arrive
// as events.UserAbout.
const aboutRefetchInterval = 24 * time.Hour

func aboutExtraProfile(status string) database.ExtraProfile {
	var profile database.ExtraProfile
	// The value is a string, which always marshals.
	_ = profile.Set(aboutProfileKey, status)
	return profile
}

func aboutFetchDue(lastFetch, now time.Time) bool {
	return now.Sub(lastFetch) >= aboutRefetchInterval
}

// aboutUserInfo is the ghost info that carries an about text.
func aboutUserInfo(status string, at time.Time) *bridgev2.UserInfo {
	return &bridgev2.UserInfo{
		ExtraProfile: aboutExtraProfile(status),
		ExtraUpdates: markAboutFetched(at),
	}
}

func markAboutFetched(at time.Time) bridgev2.ExtraUpdater[*bridgev2.Ghost] {
	return func(_ context.Context, ghost *bridgev2.Ghost) bool {
		ghost.Metadata.(*waid.GhostMetadata).AboutFetched = jsontime.U(at)
		return true
	}
}

func canHaveAbout(jid types.JID) bool {
	return jid.Server == types.DefaultUserServer || jid.Server == types.HiddenUserServer
}

// aboutNeedsFetch says if the ghost's about text is due for a fetch.
func aboutNeedsFetch(ghost *bridgev2.Ghost, now time.Time) bool {
	return canHaveAbout(waid.ParseUserID(ghost.ID)) &&
		aboutFetchDue(ghost.Metadata.(*waid.GhostMetadata).AboutFetched.Time, now)
}

// fetchGhostAbout fetches the ghost's about text if it wasn't fetched in the last day. The attempt counts
// even when it fails, so an unreachable user is not asked again on every event.
func (wa *WhatsAppClient) fetchGhostAbout(ctx context.Context, ghost *bridgev2.Ghost) bool {
	now := time.Now()
	if !aboutNeedsFetch(ghost, now) {
		return false
	}
	jid := waid.ParseUserID(ghost.ID)
	ghost.Metadata.(*waid.GhostMetadata).AboutFetched = jsontime.U(now)
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	infos, err := wa.Client.GetUserInfo(fetchCtx, []types.JID{jid})
	if err != nil {
		wa.UserLogin.Log.Debug().Err(err).Stringer("jid", jid).Msg("Failed to fetch about text")
		return true
	}
	if info, ok := infos[jid]; ok {
		ghost.UpdateContactInfo(ctx, nil, nil, aboutExtraProfile(info.Status))
	}
	return true
}

func (wa *WhatsAppClient) handleWAUserAbout(ctx context.Context, evt *events.UserAbout) {
	if !canHaveAbout(evt.JID) {
		return
	}
	ghost, err := wa.Main.Bridge.GetGhostByID(ctx, waid.MakeUserID(evt.JID))
	if err != nil || ghost == nil {
		wa.UserLogin.Log.Err(err).Stringer("jid", evt.JID).Msg("Failed to get ghost to update about text")
		return
	}
	ghost.UpdateInfo(ctx, aboutUserInfo(evt.Status, time.Now()))
}
