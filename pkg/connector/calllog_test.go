package connector

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/calllog"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
)

var (
	testCallTime   = time.Unix(1_700_000_000, 0)
	testCaller     = types.NewJID("15550100", types.DefaultUserServer)
	testCallGroup  = types.NewJID("120363000000000001", types.GroupServer)
	testCallPortal = networkid.PortalKey{ID: "15550100@s.whatsapp.net", Receiver: "login"}
)

func testMkCall(meta *types.BasicCallMeta) (calllog.Call, bool) {
	return calllog.Call{Portal: testCallPortal, Started: meta.Timestamp}, true
}

func callText(t *testing.T, evt bridgev2.RemoteEvent) string {
	t.Helper()
	require.NotNil(t, evt)
	return evt.(*simplevent.Message[*calllog.Call]).Data.Text()
}

func callMeta(id string, at time.Duration) types.BasicCallMeta {
	return types.BasicCallMeta{CallID: id, CallCreator: testCaller, Timestamp: testCallTime.Add(at)}
}

func TestCallLogAnsweredCall(t *testing.T) {
	l := calllog.New()
	start := callLogReport(l, &events.CallOffer{BasicCallMeta: callMeta("c1", 0), Video: true}, testMkCall)
	assert.Equal(t, bridgev2.RemoteEventMessage, start.GetType())
	assert.Equal(t, "Incoming video call", callText(t, start))
	assert.Equal(t, "Video call in progress", callText(t, callLogReport(l, &events.CallAccept{BasicCallMeta: callMeta("c1", 4*time.Second)}, testMkCall)))
	end := callLogReport(l, &events.CallTerminate{BasicCallMeta: callMeta("c1", 64*time.Second)}, testMkCall)
	assert.Equal(t, bridgev2.RemoteEventEdit, end.GetType())
	assert.Equal(t, "Video call, 1:00", callText(t, end))
}

func TestCallLogMissedAndDeclined(t *testing.T) {
	l := calllog.New()
	callLogReport(l, &events.CallOffer{BasicCallMeta: callMeta("m", 0)}, testMkCall)
	assert.Equal(t, "Missed voice call", callText(t, callLogReport(l, &events.CallTerminate{BasicCallMeta: callMeta("m", 20*time.Second)}, testMkCall)))

	callLogReport(l, &events.CallOffer{BasicCallMeta: callMeta("d", 0)}, testMkCall)
	assert.Equal(t, "Declined voice call", callText(t, callLogReport(l, &events.CallReject{BasicCallMeta: callMeta("d", time.Second)}, testMkCall)))
}

func TestCallLogGroupAndNotice(t *testing.T) {
	l := calllog.New()
	meta := callMeta("g", 0)
	meta.GroupJID = testCallGroup
	assert.Equal(t, "Incoming group voice call", callText(t, callLogReport(l, &events.CallOffer{BasicCallMeta: meta}, testMkCall)))

	assert.Equal(t, "Incoming video call", callText(t, callLogReport(l, &events.CallOfferNotice{BasicCallMeta: callMeta("n", 0), Media: "video"}, testMkCall)))
}

func TestCallLogNoDoublePost(t *testing.T) {
	l := calllog.New()
	require.NotNil(t, callLogReport(l, &events.CallOffer{BasicCallMeta: callMeta("x", 0)}, testMkCall))
	assert.Nil(t, callLogReport(l, &events.CallOfferNotice{BasicCallMeta: callMeta("x", 0)}, testMkCall), "the notice of an offered call posts nothing")
	assert.Nil(t, callLogReport(l, &events.CallTerminate{BasicCallMeta: callMeta("unknown", 0)}, testMkCall), "an unknown call has nothing to update")
}

func TestCallLogSkippedOffer(t *testing.T) {
	l := calllog.New()
	skip := func(*types.BasicCallMeta) (calllog.Call, bool) { return calllog.Call{}, false }
	assert.Nil(t, callLogReport(l, &events.CallOffer{BasicCallMeta: callMeta("s", 0)}, skip))
}

func TestRingingCalls(t *testing.T) {
	var r ringingCalls
	_, ok := r.get(testCallPortal)
	assert.False(t, ok)
	r.set(testCallPortal, ringingCall{ID: "c1", From: testCaller})
	got, ok := r.get(testCallPortal)
	require.True(t, ok)
	assert.Equal(t, "c1", got.ID)
	r.clear("other")
	_, ok = r.get(testCallPortal)
	assert.True(t, ok)
	r.clear("c1")
	_, ok = r.get(testCallPortal)
	assert.False(t, ok)
}
