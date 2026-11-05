package ghsecret

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func TestUpdateSealsSecretForGitHub(t *testing.T) {
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	updated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-pat" {
			t.Errorf("missing authorization")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/owner/repo/actions/secrets/public-key":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"key_id": "key-id",
				"key":    base64.StdEncoding.EncodeToString(publicKey[:]),
			})
		case r.Method == http.MethodPut && r.URL.Path == "/repos/owner/repo/actions/secrets/NTE_REFRESH_TOKEN":
			var payload struct {
				EncryptedValue string `json:"encrypted_value"`
				KeyID          string `json:"key_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode payload: %v", err)
			}
			sealed, err := base64.StdEncoding.DecodeString(payload.EncryptedValue)
			if err != nil {
				t.Errorf("decode sealed value: %v", err)
			}
			plain, ok := box.OpenAnonymous(nil, sealed, publicKey, privateKey)
			if !ok || string(plain) != "new-refresh-token" || payload.KeyID != "key-id" {
				t.Errorf("secret was not sealed correctly")
			}
			updated = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	err = Update(context.Background(), server.Client(), server.URL, "owner/repo", "test-pat", "NTE_REFRESH_TOKEN", "new-refresh-token")
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Fatal("secret update endpoint was not called")
	}
}

func TestUpdateRejectsInvalidRepository(t *testing.T) {
	if err := Update(context.Background(), nil, "https://api.github.com", "invalid", "token", "name", "value"); err == nil {
		t.Fatal("expected invalid repository error")
	}
}
