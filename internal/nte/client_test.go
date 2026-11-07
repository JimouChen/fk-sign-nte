package nte

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestGenerateDSProtocolVector(t *testing.T) {
	client := NewClient()
	client.Now = func() time.Time { return time.Unix(1710000000, 0) }
	client.Random = bytes.NewReader([]byte{26, 27, 28, 29, 30, 31, 32, 33})
	got, err := client.generateDS("1.2.2")
	if err != nil {
		t.Fatal(err)
	}
	want := "1710000000,ABCDEFGH,6cf4e2edb3dc484539a2b8d90080c2db"
	if got != want {
		t.Fatalf("generateDS() = %q, want %q", got, want)
	}
}

func TestSignSuccessReturnsRewards(t *testing.T) {
	calls := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.Header.Get("appversion") != appVersion || r.Header.Get("platform") != "android" {
			t.Errorf("missing common headers on %s", r.URL.Path)
		}
		if matched, _ := regexp.MatchString(`^\d+,[A-Za-z0-9]{8},[0-9a-f]{32}$`, r.Header.Get("ds")); !matched {
			t.Errorf("invalid ds header on %s: %q", r.URL.Path, r.Header.Get("ds"))
		}
		switch r.URL.Path {
		case refreshTokenPath:
			if r.Header.Get("authorization") != "old-refresh" || r.Header.Get("uid") != "10000000" {
				t.Errorf("unexpected refresh headers")
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
				"accessToken": "access", "refreshToken": "new-refresh", "uid": 10001,
			}})
		case gameRolesPath:
			if r.Header.Get("authorization") != "access" || r.URL.Query().Get("gameId") != "1289" {
				t.Errorf("unexpected role request")
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
				"roles": []map[string]any{{"roleId": "role-1", "roleName": "测试角色"}},
			}})
		case communitySignPath:
			if err := r.ParseForm(); err != nil || r.Form.Get("communityId") != "1" {
				t.Errorf("unexpected community form: %v", r.Form)
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"exp": 10, "goldCoin": "20"}})
		case gameSignPath:
			if err := r.ParseForm(); err != nil || r.Form.Get("roleId") != "role-1" || r.Form.Get("gameId") != "1289" {
				t.Errorf("unexpected game form: %v", r.Form)
			}
			writeJSON(t, w, map[string]any{"code": 0})
		case gameStatePath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"todaySign": true, "days": "2"}})
		case gameRewardsPath:
			if r.URL.Query().Get("roleId") != "role-1" {
				t.Errorf("reward request missing roleId")
			}
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
				"items": []map[string]any{{"name": "初级奖励"}, {"itemName": "稀有核心", "quantity": 3}},
			}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			writeJSON(t, w, map[string]any{"code": 1, "msg": "not found"})
		}
	}))
	defer server.Close()

	report, err := testClient(server).Sign(context.Background(), Account{
		RefreshToken: "old-refresh",
		DeviceID:     "device",
		GameID:       DefaultGameID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() || report.UID != "10001" || report.NewRefreshToken != "new-refresh" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Community.Reward != "10 经验、20 金币" {
		t.Fatalf("unexpected community reward: %q", report.Community.Reward)
	}
	if len(report.Roles) != 1 || report.Roles[0].Outcome.Reward != "稀有核心×3" {
		t.Fatalf("unexpected game reward: %+v", report.Roles)
	}
	for _, path := range []string{refreshTokenPath, gameRolesPath, communitySignPath, gameSignPath, gameStatePath, gameRewardsPath} {
		if calls[path] != 1 {
			t.Errorf("%s called %d times, want 1", path, calls[path])
		}
	}
}

func TestSignAlreadySignedStillReturnsTodayReward(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case refreshTokenPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
				"accessToken": "access", "refreshToken": "refresh", "uid": "uid",
			}})
		case gameRolesPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{
				"roles": []map[string]any{{"roleId": "role-1"}},
			}})
		case communitySignPath, gameSignPath:
			writeJSON(t, w, map[string]any{"code": 1, "msg": "今日已签到"})
		case gameStatePath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"todaySign": true, "days": 1}})
		case gameRewardsPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": []map[string]any{{"name": "每日补给", "count": 2}}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	report, err := testClient(server).Sign(context.Background(), Account{RefreshToken: "refresh", DeviceID: "device"})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK() || !report.Community.AlreadySigned || !report.Roles[0].Outcome.AlreadySigned {
		t.Fatalf("already-signed responses should be successful: %+v", report)
	}
	if report.NewRefreshToken != "" || report.Roles[0].Outcome.Reward != "每日补给×2" {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestAlreadySignedRequiresStateConfirmation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case refreshTokenPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"accessToken": "access", "refreshToken": "refresh", "uid": "uid"}})
		case gameRolesPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"roles": []map[string]any{{"roleId": "role-1"}}}})
		case communitySignPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"exp": 1, "goldCoin": 1}})
		case gameSignPath:
			writeJSON(t, w, map[string]any{"code": 1, "msg": "重复签到"})
		case gameStatePath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"todaySign": false, "days": 0}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	report, err := testClient(server).Sign(context.Background(), Account{RefreshToken: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK() || report.Roles[0].Outcome.Success {
		t.Fatalf("unconfirmed already-signed response must fail: %+v", report)
	}
}

func TestRefreshTokenExpiredEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer server.Close()

	_, err := testClient(server).Sign(context.Background(), Account{RefreshToken: "expired"})
	if err == nil || !strings.Contains(err.Error(), "refreshToken 已失效，请重新登录") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "返回空响应") {
		t.Fatalf("HTTP 402 was reported as a generic empty response: %v", err)
	}
}

func TestNonJSONResponseDoesNotLeakBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, "sdk-token-secret <html>private</html>")
	}))
	defer server.Close()

	_, err := testClient(server).Sign(context.Background(), Account{RefreshToken: "refresh"})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
		t.Fatalf("response body leaked in error: %v", err)
	}
}

func TestRoleIDFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case refreshTokenPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"accessToken": "access", "refreshToken": "refresh", "uid": "uid"}})
		case gameRolesPath:
			writeJSON(t, w, map[string]any{"code": 1, "msg": "temporarily unavailable"})
		case communitySignPath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{}})
		case gameSignPath:
			_ = r.ParseForm()
			if r.Form.Get("roleId") != "manual-role" {
				t.Errorf("fallback role not used: %v", r.Form)
			}
			writeJSON(t, w, map[string]any{"code": 0})
		case gameStatePath:
			writeJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"todaySign": true, "days": 0}})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	report, err := testClient(server).Sign(context.Background(), Account{RefreshToken: "refresh", RoleID: "manual-role"})
	if err != nil || !report.OK() || report.Roles[0].RoleID != "manual-role" {
		t.Fatalf("role fallback failed: report=%+v err=%v", report, err)
	}
}

func testClient(server *httptest.Server) *Client {
	client := NewClient()
	client.BaseURL = server.URL
	client.HTTPClient = server.Client()
	client.Random = bytes.NewReader(make([]byte, 4096))
	return client
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
