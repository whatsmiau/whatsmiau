package whatsmiau

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestStatusAudience(t *testing.T) {
	saved := func(user string) types.JID { return types.NewJID(user, types.DefaultUserServer) }
	contacts := map[types.JID]types.ContactInfo{
		saved("5511900000001"): {Found: true, FullName: "Ana"},
		saved("5511900000002"): {Found: true, FullName: "Bia"},
		saved("5511900000003"): {Found: true, FullName: "Caio"},
		// Known from a push name only: not in the address book, whatsmeow leaves it out.
		saved("5511900000004"): {Found: true, PushName: "Davi"},
	}
	for name, tc := range map[string]struct {
		privacy []types.StatusPrivacy
		want    StatusAudience
	}{
		"my contacts": {
			[]types.StatusPrivacy{{Type: types.StatusPrivacyTypeContacts, IsDefault: true}},
			StatusAudience{Privacy: types.StatusPrivacyTypeContacts, Recipients: 3},
		},
		"no answer from the server": {
			nil,
			StatusAudience{Privacy: types.StatusPrivacyTypeContacts, Recipients: 3},
		},
		"my contacts except": {
			[]types.StatusPrivacy{{Type: types.StatusPrivacyTypeBlacklist, List: []types.JID{saved("5511900000002"), saved("5511999999999")}}},
			StatusAudience{Privacy: types.StatusPrivacyTypeBlacklist, ListSize: 2, Recipients: 2},
		},
		"only share with": {
			[]types.StatusPrivacy{{Type: types.StatusPrivacyTypeWhitelist, List: []types.JID{saved("5511900000001"), saved("5511988888888")}}},
			StatusAudience{Privacy: types.StatusPrivacyTypeWhitelist, ListSize: 2, Recipients: 2},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := statusAudience(tc.privacy, contacts); *got != tc.want {
				t.Fatalf("statusAudience = %+v, want %+v", *got, tc.want)
			}
		})
	}
}
