package whatsmiau

import (
	"context"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// StatusAudience says who a status sent now with SendStatus would reach. It only carries counts:
// the contacts themselves are never returned.
type StatusAudience struct {
	// Privacy is the default status privacy set on the phone: contacts (my contacts), blacklist
	// (my contacts except...) or whitelist (only share with...).
	Privacy types.StatusPrivacyType
	// ListSize is the number of people in the except or only-share-with list.
	ListSize int
	// Recipients is the number of people the status goes to, the account's own devices aside.
	Recipients int
}

// StatusAudience reads the status privacy and the contact list the next status would be sent with.
func (s *Whatsmiau) StatusAudience(ctx context.Context, instanceID string) (*StatusAudience, error) {
	client, ok := s.clients.Load(instanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}
	if client.Store.GetJID().IsEmpty() {
		return nil, whatsmeow.ErrNotLoggedIn
	}
	if !client.IsConnected() || !client.IsLoggedIn() {
		return nil, ErrInstanceNotConnected
	}
	privacy, err := client.GetStatusPrivacy(ctx)
	if err != nil {
		return nil, err
	}
	contacts, err := client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	return statusAudience(privacy, contacts), nil
}

// statusAudience counts the recipients the way whatsmeow picks them when sending to
// status@broadcast (getStatusBroadcastRecipients): the only-share-with list as it is, or the
// contacts with a saved name (FullName), minus the except list.
func statusAudience(privacy []types.StatusPrivacy, contacts map[types.JID]types.ContactInfo) *StatusAudience {
	setting := types.StatusPrivacy{Type: types.StatusPrivacyTypeContacts}
	if len(privacy) > 0 {
		setting = privacy[0]
	}
	audience := &StatusAudience{Privacy: setting.Type, ListSize: len(setting.List)}
	if setting.Type == types.StatusPrivacyTypeWhitelist {
		audience.Recipients = len(setting.List)
		return audience
	}
	excluded := make(map[types.JID]struct{}, len(setting.List))
	if setting.Type == types.StatusPrivacyTypeBlacklist {
		for _, jid := range setting.List {
			excluded[jid] = struct{}{}
		}
	}
	for jid, contact := range contacts {
		if _, skip := excluded[jid]; skip || contact.FullName == "" {
			continue
		}
		audience.Recipients++
	}
	return audience
}
