package whatsmiau

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/verbeux-ai/whatsmiau/models"
)

var (
	testStatusViewer = types.NewJID("111111111111111", types.HiddenUserServer)
	testStatusAt     = time.Unix(1794666450, 0)
)

// statusEventsHarness runs events through the instance handlers and collects what would be posted
// to the webhook, as JSON.
func statusEventsHarness(t *testing.T, events ...string) (*Whatsmiau, *models.Instance, func() []map[string]any) {
	t.Helper()
	s := &Whatsmiau{clients: xsync.NewMap[string, *whatsmeow.Client](), emitter: make(chan emitter, 10)}
	s.clients.Store("inst", &whatsmeow.Client{})
	enabled := true
	instance := &models.Instance{ID: "inst", Webhook: models.InstanceWebhook{Enabled: &enabled, Url: "http://webhook.test", Events: events}}
	collect := func() []map[string]any {
		var out []map[string]any
		for {
			select {
			case e := <-s.emitter:
				raw, err := json.Marshal(e.data)
				if err != nil {
					t.Fatalf("marshal webhook: %v", err)
				}
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatalf("unmarshal webhook: %v", err)
				}
				out = append(out, body)
			default:
				return out
			}
		}
	}
	return s, instance, collect
}

// incoming builds the event whatsmeow dispatches for a decrypted message.
func incoming(chat types.JID, id string, msg *waE2E.Message) *events.Message {
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: testStatusViewer, IsGroup: chat.Server != types.DefaultUserServer},
			ID:            id,
			Timestamp:     testStatusAt,
		},
		RawMessage: msg,
	}
	return evt.UnwrapRaw()
}

func statusReaction(chat types.JID, statusID, emoji string) *waE2E.Message {
	return &waE2E.Message{
		ReactionMessage: &waE2E.ReactionMessage{
			Key:  &waCommon.MessageKey{RemoteJID: proto.String(chat.String()), FromMe: proto.Bool(true), ID: proto.String(statusID)},
			Text: proto.String(emoji),
		},
		MessageContextInfo: &waE2E.MessageContextInfo{},
	}
}

func path(body map[string]any, keys ...string) any {
	var cur any = body
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[k]
	}
	return cur
}

// A like on one of the instance's own status updates comes from status@broadcast; it is the only
// thing from that chat the webhook carries.
func TestStatusReactionIsEmittedAsReactionMessage(t *testing.T) {
	s, instance, collect := statusEventsHarness(t, "MESSAGES_UPSERT")
	s.handleMessageEvent("inst", instance, incoming(types.StatusBroadcastJID, "A566", statusReaction(types.StatusBroadcastJID, "3EB0STATUS", "💚")), enabledWebhookEvents(instance.Webhook))

	got := collect()
	if len(got) != 1 {
		t.Fatalf("got %d webhooks, want 1: %v", len(got), got)
	}
	for _, check := range []struct {
		path []string
		want any
	}{
		{[]string{"event"}, "messages.upsert"},
		{[]string{"data", "messageType"}, "reactionMessage"},
		{[]string{"data", "key", "remoteJid"}, "status@broadcast"},
		{[]string{"data", "key", "participant"}, testStatusViewer.String()},
		{[]string{"data", "message", "reactionMessage", "text"}, "💚"},
	} {
		if v := path(got[0], check.path...); v != check.want {
			t.Errorf("%v = %v, want %v", check.path, v, check.want)
		}
	}
	if v := path(got[0], "data", "message", "reactionMessage", "key", "id"); v != "3EB0STATUS" {
		t.Errorf("reaction key id = %v, want the status id", v)
	}
	if v := path(got[0], "data", "message", "reactionMessage", "key", "remoteJid"); v != "status@broadcast" {
		t.Errorf("reaction key remoteJid = %v, want status@broadcast", v)
	}
}

func TestGroupStatusReactionIsEmittedAsReactionMessage(t *testing.T) {
	s, instance, collect := statusEventsHarness(t, "MESSAGES_UPSERT")
	group := types.NewJID("120363000000000001", types.GroupServer)
	s.handleMessageEvent("inst", instance, incoming(group, "A50E", statusReaction(group, "3EB0GROUPSTATUS", "💚")), enabledWebhookEvents(instance.Webhook))

	got := collect()
	if len(got) != 1 || path(got[0], "data", "messageType") != "reactionMessage" {
		t.Fatalf("webhooks = %v, want one reactionMessage", got)
	}
	if v := path(got[0], "data", "message", "reactionMessage", "key", "id"); v != "3EB0GROUPSTATUS" {
		t.Errorf("reaction key id = %v, want the group status id", v)
	}
}

// The status updates contacts post are not carried, whatever their content.
func TestStatusUpdatesOfContactsAreNotEmitted(t *testing.T) {
	s, instance, collect := statusEventsHarness(t, "MESSAGES_UPSERT", "MESSAGES_DELETE")
	for name, msg := range map[string]*waE2E.Message{
		"text":  {ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("bom dia")}},
		"image": {ImageMessage: &waE2E.ImageMessage{Caption: proto.String("foto")}},
		"revoke": {ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(),
			Key: &waCommon.MessageKey{RemoteJID: proto.String("status@broadcast"), ID: proto.String("X")}}},
	} {
		s.handleMessageEvent("inst", instance, incoming(types.StatusBroadcastJID, "S-"+name, msg), enabledWebhookEvents(instance.Webhook))
	}
	if got := collect(); len(got) != 0 {
		t.Fatalf("status updates of contacts reached the webhook: %v", got)
	}
}

// A group stanza can bring the sender key in a pairwise part, which whatsmeow emits as a message of
// its own next to the real content. It used to reach the webhook as messageType "unknown".
func TestKeyDistributionOnlyIsNotEmitted(t *testing.T) {
	s, instance, collect := statusEventsHarness(t, "MESSAGES_UPSERT")
	group := types.NewJID("120363000000000001", types.GroupServer)
	skdm := &waE2E.SenderKeyDistributionMessage{GroupID: proto.String(group.String()), AxolotlSenderKeyDistributionMessage: []byte{1, 2, 3}}

	s.handleMessageEvent("inst", instance, incoming(group, "K1", &waE2E.Message{SenderKeyDistributionMessage: skdm, MessageContextInfo: &waE2E.MessageContextInfo{}}), enabledWebhookEvents(instance.Webhook))
	if got := collect(); len(got) != 0 {
		t.Fatalf("a sender key distribution reached the webhook: %v", got)
	}

	// With content next to it, the content is emitted as before.
	s.handleMessageEvent("inst", instance, incoming(group, "K2", &waE2E.Message{SenderKeyDistributionMessage: skdm, Conversation: proto.String("oi")}), enabledWebhookEvents(instance.Webhook))
	got := collect()
	if len(got) != 1 || path(got[0], "data", "messageType") != "conversation" {
		t.Fatalf("webhooks = %v, want one conversation", got)
	}
}

// Whoever views a status sends a read receipt from status@broadcast (or the group), which the
// webhook carries as messages.update READ with the viewer as participant.
func TestStatusViewIsEmittedAsRead(t *testing.T) {
	s, instance, collect := statusEventsHarness(t, "MESSAGES_UPDATE")
	group := types.NewJID("120363000000000001", types.GroupServer)
	for _, chat := range []types.JID{types.StatusBroadcastJID, group} {
		s.handleReceiptEvent("inst", instance, &events.Receipt{
			MessageSource: types.MessageSource{Chat: chat, Sender: testStatusViewer, IsGroup: true},
			MessageIDs:    []types.MessageID{"3EB0STATUS"},
			Timestamp:     testStatusAt,
			Type:          types.ReceiptTypeRead,
		}, enabledWebhookEvents(instance.Webhook))
	}
	got := collect()
	if len(got) != 2 {
		t.Fatalf("got %d webhooks, want 2: %v", len(got), got)
	}
	for i, chat := range []string{"status@broadcast", group.String()} {
		if path(got[i], "event") != "messages.update" || path(got[i], "data", "status") != "READ" ||
			path(got[i], "data", "keyId") != "3EB0STATUS" || path(got[i], "data", "remoteJid") != chat ||
			path(got[i], "data", "participant") != testStatusViewer.String() {
			t.Errorf("webhook %d = %v", i, got[i])
		}
	}
}
