package oauth

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// client-metadata.json is ctxtra's client ID metadata document, to be
// hosted at its client_id once the domain exists. ClientMetadataURL stays
// empty until then, so sign-in uses dynamic registration.
func TestClientMetadataDocument(t *testing.T) {
	data, err := os.ReadFile("client-metadata.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ClientID     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
		Application  string   `json:"application_type"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ClientName != "ctxtra" || !slices.Equal(doc.RedirectURIs, []string{"http://127.0.0.1/callback"}) ||
		!slices.Equal(doc.GrantTypes, []string{"authorization_code", "refresh_token"}) ||
		doc.AuthMethod != "none" || doc.Application != "native" {
		t.Errorf("document: %+v", doc)
	}
	if ClientMetadataURL != "" && doc.ClientID != ClientMetadataURL {
		t.Errorf("client_id %s, but ClientMetadataURL is %s", doc.ClientID, ClientMetadataURL)
	}
}
