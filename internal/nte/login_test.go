package nte

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestSignLoginParamsSortsKeys(t *testing.T) {
	params := url.Values{"b": {"2"}, "a": {"1"}}
	signLoginParams(params)
	digest := md5.Sum([]byte("12" + loginSecret))
	if got, want := params.Get("sign"), hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("sign = %q, want %q", got, want)
	}
}

func TestSMSLoginFlow(t *testing.T) {
	calls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Header.Get("platform") != "android" {
			t.Errorf("missing platform header on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case sendCodePath:
			if r.Form.Get("cellphone") != "13800000000" || r.Form.Get("deviceId") != "device" || r.Form.Get("t") != "1710000000" {
				t.Errorf("unexpected send-code form: %v", r.Form)
			}
			if r.Header.Get("ds") != "" {
				t.Errorf("user endpoint must not include ds")
			}
			writeJSON(t, w, map[string]any{"code": 0})
		case checkCodePath:
			if r.Form.Get("captcha") != "123456" || r.Form.Get("cellphone") != "13800000000" {
				t.Errorf("unexpected check-code form: %v", r.Form)
			}
			writeJSON(t, w, map[string]any{"code": 0})
		case sdkLoginPath:
			if got := decryptLoginValue(t, r.Form.Get("cellphone")); got != "13800000000" {
				t.Errorf("decrypted phone = %q", got)
			}
			if got := decryptLoginValue(t, r.Form.Get("captcha")); got != "123456" {
				t.Errorf("decrypted code = %q", got)
			}
			if r.Form.Get("t") != "1710000000000" || len(r.Form.Get("sign")) != 32 {
				t.Errorf("unexpected sdk login form: %v", r.Form)
			}
			writeJSON(t, w, map[string]any{"code": 0, "result": map[string]any{"token": "sdk-token", "userId": 90001}})
		case userCenterLoginPath:
			if r.Form.Get("token") != "sdk-token" || r.Form.Get("userIdentity") != "90001" || r.Form.Get("appId") != userCenterAppID {
				t.Errorf("unexpected user-center form: %v", r.Form)
			}
			if r.Header.Get("ds") == "" || r.Header.Get("appversion") != appVersion || r.Header.Get("deviceid") != "device" {
				t.Errorf("missing user-center headers")
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
				"accessToken": "access", "refreshToken": "refresh", "uid": 10001,
			}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := testClient(server)
	client.UserBaseURL = server.URL
	client.Now = func() time.Time { return time.Unix(1710000000, 0) }
	if err := client.SendLoginCode(context.Background(), "13800000000", "device"); err != nil {
		t.Fatal(err)
	}
	account, err := client.LoginBySMS(context.Background(), "13800000000", "123456", "device")
	if err != nil {
		t.Fatal(err)
	}
	if account.RefreshToken != "refresh" || account.UID != "10001" || account.DeviceID != "device" || account.GameID != DefaultGameID {
		t.Fatalf("unexpected account: %+v", account)
	}
	for _, path := range []string{sendCodePath, checkCodePath, sdkLoginPath, userCenterLoginPath} {
		if calls[path] != 1 {
			t.Errorf("%s called %d times, want 1", path, calls[path])
		}
	}
}

func TestLoginRejectsInvalidInput(t *testing.T) {
	client := NewClient()
	if err := client.SendLoginCode(context.Background(), "invalid", "device"); err == nil {
		t.Fatal("expected invalid phone error")
	}
	if _, err := client.LoginBySMS(context.Background(), "13800000000", "abc", "device"); err == nil {
		t.Fatal("expected invalid code error")
	}
}

func decryptLoginValue(t *testing.T, encoded string) string {
	t.Helper()
	encrypted, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode encrypted value: %v", err)
	}
	block, err := aes.NewCipher([]byte(loginSecret[len(loginSecret)-16:]))
	if err != nil {
		t.Fatal(err)
	}
	if len(encrypted) == 0 || len(encrypted)%block.BlockSize() != 0 {
		t.Fatalf("invalid encrypted length: %d", len(encrypted))
	}
	plain := make([]byte, len(encrypted))
	for offset := 0; offset < len(encrypted); offset += block.BlockSize() {
		block.Decrypt(plain[offset:offset+block.BlockSize()], encrypted[offset:offset+block.BlockSize()])
	}
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > block.BlockSize() || padding > len(plain) {
		t.Fatalf("invalid padding: %d", padding)
	}
	return string(plain[:len(plain)-padding])
}
