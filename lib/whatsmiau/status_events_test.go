package whatsmiau

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
	"go.mau.fi/whatsmeow"
	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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
func incoming(stanzaTag string, chat types.JID, id string, msg *waE2E.Message) *events.Message {
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: testStatusViewer, IsGroup: chat.Server != types.DefaultUserServer},
			ID:            id,
			Timestamp:     testStatusAt,
			StanzaTag:     stanzaTag,
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
	s.handleMessageEvent("inst", instance, incoming("status", types.StatusBroadcastJID, "A566", statusReaction(types.StatusBroadcastJID, "3EB0STATUS", "💚")), enabledWebhookEvents(instance.Webhook))

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
	s.handleMessageEvent("inst", instance, incoming("status", group, "A50E", statusReaction(group, "3EB0GROUPSTATUS", "💚")), enabledWebhookEvents(instance.Webhook))

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
	s, instance, collect := statusEventsHarness(t, "MESSAGES_UPSERT", "MESSAGES_DELETE", "MESSAGES_EDITED")
	statusKey := &waCommon.MessageKey{RemoteJID: proto.String("status@broadcast"), ID: proto.String("X")}
	for name, msg := range map[string]*waE2E.Message{
		"text":   {ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("bom dia")}},
		"image":  {ImageMessage: &waE2E.ImageMessage{Caption: proto.String("foto")}},
		"revoke": {ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_REVOKE.Enum(), Key: statusKey}},
		"edit": {ProtocolMessage: &waE2E.ProtocolMessage{Type: waE2E.ProtocolMessage_MESSAGE_EDIT.Enum(), Key: statusKey,
			EditedMessage: &waE2E.Message{Conversation: proto.String("boa tarde")}}},
	} {
		s.handleMessageEvent("inst", instance, incoming("status", types.StatusBroadcastJID, "S-"+name, msg), enabledWebhookEvents(instance.Webhook))
	}
	if got := collect(); len(got) != 0 {
		t.Fatalf("status updates of contacts reached the webhook: %v", got)
	}
}

type testSenderKeys map[[2]string][]byte

func (keys testSenderKeys) PutSenderKey(_ context.Context, group, sender string, key []byte) error {
	keys[[2]string{group, sender}] = key
	return nil
}

func (keys testSenderKeys) GetSenderKey(_ context.Context, group, sender string) ([]byte, error) {
	return keys[[2]string{group, sender}], nil
}

func TestIncomingStatusFrames(t *testing.T) {
	// Fixed wire frames let different Whatsmeow versions replay the same ciphertext.
	// Status broadcasts can arrive in both status and message frames.
	var fixture struct {
		Sender types.JID
		Keys   []struct {
			Chat         types.JID
			Sender       types.JID
			Distribution []byte
		}
		Frames map[string][]byte
	}
	data, err := os.ReadFile("testdata/incoming_status_frames.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	ownJID := types.NewJID("222222222222222", types.DefaultUserServer)
	client := whatsmeow.NewClient(&store.Device{ID: &ownJID, SenderKeys: testSenderKeys{}}, nil)
	client.SynchronousAck = true
	wire := client.DangerousInternals()
	for _, key := range fixture.Keys {
		sender := key.Sender
		if sender.IsEmpty() {
			sender = fixture.Sender
		}
		wire.HandleSenderKeyDistributionMessage(t.Context(), key.Chat, sender, key.Distribution)
	}

	cases := []struct {
		id        string
		stanzaTag string
		wantRead  bool
		wantType  string
		wantText  string
	}{
		{"contact-status", "status", false, "", ""},
		{"contact-status-reaction", "status", false, "reactionMessage", ""},
		{"contact-status-revoke", "status", false, "", ""},
		{"contact-message-post", "message", true, "", ""},
		{"contact-message-reaction", "message", true, "", ""},
		{"contact-message-revoke", "message", false, "", ""},
		{"group-status", "status", false, "", ""},
		{"group-status-reaction", "status", false, "reactionMessage", ""},
		{"group-message", "message", true, "conversation", "oi"},
		{"group-reaction", "message", true, "reactionMessage", "❤️"},
		{"channel-post", "status", true, "conversation", "oi"},
	}
	received := make(chan any, len(fixture.Frames))
	client.AddEventHandler(func(evt any) { received <- evt })
	queue := make(chan *waBinary.Node, len(fixture.Frames))
	for _, tt := range cases {
		frame := fixture.Frames[tt.id]
		if len(frame) == 0 {
			t.Fatalf("missing frame %q", tt.id)
		}
		wire.HandleFrame(t.Context(), frame, queue)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	done := make(chan struct{})
	go wire.HandlerQueueLoop(ctx, ctx, queue, done)
	defer func() {
		cancel()
		<-done
	}()

	// The final newsletter arrives on both pins, after all preceding frames have run.
	messages := make(map[string]*events.Message)
	for messages["channel-post"] == nil {
		select {
		case evt := <-received:
			msg, ok := evt.(*events.Message)
			if !ok {
				t.Fatalf("unexpected event %T", evt)
			}
			if messages[msg.Info.ID] != nil {
				t.Fatalf("duplicate message %s", msg.Info.ID)
			}
			messages[msg.Info.ID] = msg
		case <-ctx.Done():
			t.Fatalf("waiting for channel post: %v", ctx.Err())
		}
	}
	for _, tt := range cases {
		t.Run(tt.id, func(t *testing.T) {
			msg := messages[tt.id]
			if msg == nil {
				t.Fatalf("Whatsmeow did not dispatch %s", tt.id)
			}
			if msg.Info.StanzaTag != tt.stanzaTag {
				t.Fatalf("stanza tag = %q, want %q", msg.Info.StanzaTag, tt.stanzaTag)
			}
			s, instance, collect := statusEventsHarness(t, "MESSAGES_UPSERT", "MESSAGES_DELETE", "MESSAGES_EDITED")
			s.clients.Store(instance.ID, client)
			core, logs := observer.New(zap.ErrorLevel)
			defer zap.ReplaceGlobals(zap.New(core))()
			for _, read := range []bool{false, true} {
				instance.ReadMessages = &read
				s.handleMessageEvent(instance.ID, instance, msg, enabledWebhookEvents(instance.Webhook))
				// The disconnected client returns an error for every MarkRead attempt.
				wantReads := 0
				if read && tt.wantRead {
					wantReads = 1
				}
				if got := logs.FilterMessage("failed to mark message as read").Len(); got != wantReads {
					t.Errorf("readMessages=%t: MarkRead attempts = %d, want %d", read, got, wantReads)
				}
				logs.TakeAll()
				got := collect()
				if tt.wantType != "" {
					if len(got) != 1 || path(got[0], "event") != "messages.upsert" ||
						path(got[0], "data", "messageType") != tt.wantType {
						t.Errorf("readMessages=%t: expected one %s upsert, got %v", read, tt.wantType, got)
					} else if tt.wantType == "reactionMessage" {
						gotText, _ := path(got[0], "data", "message", "reactionMessage", "text").(string)
						if gotText == "" {
							t.Errorf("readMessages=%t: reaction text is empty", read)
						} else if tt.wantText != "" && gotText != tt.wantText {
							t.Errorf("readMessages=%t: reaction text = %q, want %q", read, gotText, tt.wantText)
						}
					} else if tt.wantType == "conversation" && path(got[0], "data", "message", "conversation") != tt.wantText {
						t.Errorf("readMessages=%t: conversation = %v, want %q", read, path(got[0], "data", "message", "conversation"), tt.wantText)
					}
				} else if len(got) != 0 {
					t.Errorf("readMessages=%t: unexpected webhooks: %v", read, got)
				}
			}
		})
	}
}

func TestNonNewsletterStatusFrameClassification(t *testing.T) {
	group := types.NewJID("120363000000000001", types.GroupServer)
	content := &waE2E.Message{Conversation: proto.String("oi")}
	for _, tt := range []struct {
		name          string
		chat          types.JID
		sender        types.JID
		stanzaTag     string
		msg           *waE2E.Message
		isStatusFrame bool
	}{
		{"personal status", types.StatusBroadcastJID, testStatusViewer, "status", content, true},
		{"personal status reaction", types.StatusBroadcastJID, testStatusViewer, "status", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{}}, true},
		{"personal status message frame", types.StatusBroadcastJID, testStatusViewer, "message", content, false},
		{"personal status reaction message frame", types.StatusBroadcastJID, testStatusViewer, "message", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{}}, false},
		{"group status", group, testStatusViewer, "status", &waE2E.Message{GroupStatusMessage: &waE2E.FutureProofMessage{Message: content}}, true},
		{"group status reaction", group, testStatusViewer, "status", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{}}, true},
		{"ordinary group reaction", group, testStatusViewer, "message", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{}}, false},
		{"newsletter post", types.NewJID("120363000000000002", types.NewsletterServer), types.NewJID("120363000000000002", types.NewsletterServer), "status", content, false},
	} {
		evt := incoming(tt.stanzaTag, tt.chat, "G1", tt.msg)
		evt.Info.Sender = tt.sender
		if got := isNonNewsletterStatusFrame(evt); got != tt.isStatusFrame {
			t.Errorf("%s: isNonNewsletterStatusFrame = %t, want %t", tt.name, got, tt.isStatusFrame)
		}
	}
}

// A group stanza can bring the sender key in a pairwise part, which whatsmeow emits as a message of
// its own next to the real content. It used to reach the webhook as messageType "unknown".
func TestKeyDistributionOnlyIsNotEmitted(t *testing.T) {
	s, instance, collect := statusEventsHarness(t, "MESSAGES_UPSERT")
	group := types.NewJID("120363000000000001", types.GroupServer)
	skdm := &waE2E.SenderKeyDistributionMessage{GroupID: proto.String(group.String()), AxolotlSenderKeyDistributionMessage: []byte{1, 2, 3}}

	s.handleMessageEvent("inst", instance, incoming("message", group, "K1", &waE2E.Message{SenderKeyDistributionMessage: skdm, MessageContextInfo: &waE2E.MessageContextInfo{}}), enabledWebhookEvents(instance.Webhook))
	if got := collect(); len(got) != 0 {
		t.Fatalf("a sender key distribution reached the webhook: %v", got)
	}

	// With content next to it, the content is emitted as before.
	s.handleMessageEvent("inst", instance, incoming("message", group, "K2", &waE2E.Message{SenderKeyDistributionMessage: skdm, Conversation: proto.String("oi")}), enabledWebhookEvents(instance.Webhook))
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
