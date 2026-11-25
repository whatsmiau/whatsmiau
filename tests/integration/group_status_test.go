//go:build integration

package integration_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func groupStatusURLs(t *testing.T) []string {
	return []string{
		fmt.Sprintf("/v1/instance/%s/message/groupStatus", instanceID(t)),
		fmt.Sprintf("/v1/message/sendGroupStatus/%s", instanceID(t)),
	}
}

func TestGroupStatusValidation(t *testing.T) {
	invalid := map[string]map[string]any{
		"NotAGroup":   {"groupJid": "5511999999999@s.whatsapp.net", "type": "text", "content": "x"},
		"StatusJID":   {"groupJid": "status@broadcast", "type": "text", "content": "x"},
		"InvalidType": {"groupJid": "123456789@g.us", "type": "document", "content": "x"},
		"NoContent":   {"groupJid": "123456789@g.us", "type": "text"},
		"BadColor":    {"groupJid": "123456789@g.us", "type": "text", "content": "x", "backgroundColor": "green"},
	}
	for _, url := range groupStatusURLs(t) {
		for name, body := range invalid {
			t.Run(name+" "+url, func(t *testing.T) {
				resp := do(t, http.MethodPost, url, body)
				defer drainClose(resp)
				assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
			})
		}
	}
}

// TestGroupStatusSend posts real group statuses. It needs a group the test
// number belongs to (TEST_GROUP_STATUS_JID) and is skipped otherwise. Each
// status is deleted again unless TEST_GROUP_STATUS_KEEP=1, so the members'
// phones can be checked by hand while it runs with the flag on.
func TestGroupStatusSend(t *testing.T) {
	groupJID := os.Getenv("TEST_GROUP_STATUS_JID")
	if groupJID == "" {
		t.Skip("TEST_GROUP_STATUS_JID not set")
	}
	bodies := map[string]map[string]any{
		"Text": {"groupJid": groupJID, "type": "text", "content": "Teste de status no grupo", "backgroundColor": "#25D366", "font": 1},
	}
	if image := os.Getenv("TEST_GROUP_STATUS_IMAGE_URL"); image != "" {
		bodies["Image"] = map[string]any{"groupJid": groupJID, "type": "image", "content": image, "caption": "Teste de status no grupo"}
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			resp := do(t, http.MethodPost, groupStatusURLs(t)[1], body)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var result struct {
				Key struct {
					RemoteJid string `json:"remoteJid"`
					FromMe    bool   `json:"fromMe"`
					ID        string `json:"id"`
				} `json:"key"`
				Status      string `json:"status"`
				MessageType string `json:"messageType"`
			}
			mustDecode(t, resp, &result)
			assert.Equal(t, groupJID, result.Key.RemoteJid)
			assert.True(t, result.Key.FromMe)
			assert.NotEmpty(t, result.Key.ID)
			assert.Equal(t, "sent", result.Status)
			assert.Equal(t, "groupStatusMessageV2", result.MessageType)

			if os.Getenv("TEST_GROUP_STATUS_KEEP") == "1" {
				return
			}
			del := do(t, http.MethodDelete, fmt.Sprintf("/v1/chat/deleteMessageForEveryone/%s", instanceID(t)), map[string]any{
				"id":          result.Key.ID,
				"remoteJid":   result.Key.RemoteJid,
				"fromMe":      true,
				"groupStatus": true,
			})
			defer drainClose(del)
			assert.Equal(t, http.StatusOK, del.StatusCode)
		})
	}
}
