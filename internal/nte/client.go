package nte

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL     = "https://bbs-api.tajiduo.com"
	defaultUserBaseURL = "https://user.laohu.com"
	appVersion         = "1.1.0"
	dsSalt             = "pUds3dfMkl"
	maxResponseSize    = 1 << 20

	refreshTokenPath  = "/usercenter/api/refreshToken"
	gameRolesPath     = "/usercenter/api/v2/getGameRoles"
	communitySignPath = "/apihub/api/signin"
	gameSignPath      = "/apihub/awapi/sign"
	gameStatePath     = "/apihub/awapi/signin/state"
	gameRewardsPath   = "/apihub/awapi/sign/rewards"
)

type Client struct {
	BaseURL     string
	UserBaseURL string
	HTTPClient  *http.Client
	Now         func() time.Time
	Random      io.Reader
}

func NewClient() *Client {
	return &Client{
		BaseURL:     defaultBaseURL,
		UserBaseURL: defaultUserBaseURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		Now:    time.Now,
		Random: rand.Reader,
	}
}

func NewDeviceID() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", fmt.Errorf("生成 deviceId 失败：%w", err)
	}
	return hex.EncodeToString(value), nil
}

type role struct {
	ID         string
	Name       string
	ServerName string
}

type signState struct {
	TodaySign bool
	Days      int
}

func (c *Client) Sign(ctx context.Context, account Account) (Report, error) {
	var report Report
	if strings.TrimSpace(account.RefreshToken) == "" {
		return report, fmt.Errorf("NTE_REFRESH_TOKEN 不能为空")
	}
	if account.GameID == "" {
		account.GameID = DefaultGameID
	}

	accessToken, refreshToken, uid, err := c.refresh(ctx, account)
	if err != nil {
		return report, err
	}
	report.UID = uid
	if refreshToken != account.RefreshToken {
		report.NewRefreshToken = refreshToken
	}
	if uid == "" {
		return report, fmt.Errorf("刷新令牌后仍缺少 UID，请配置 NTE_UID")
	}

	report.Community = c.signCommunity(ctx, accessToken, uid, account.DeviceID)
	roles, roleErr := c.gameRoles(ctx, accessToken, uid, account.DeviceID, account.GameID)
	if len(roles) == 0 && account.RoleID != "" {
		roles = []role{{ID: account.RoleID}}
	}
	if len(roles) == 0 {
		if roleErr != nil {
			return report, roleErr
		}
		return report, fmt.Errorf("未找到游戏角色，请配置 NTE_ROLE_ID")
	}

	report.Roles = make([]RoleOutcome, 0, len(roles))
	for _, item := range roles {
		report.Roles = append(report.Roles, RoleOutcome{
			RoleID:   item.ID,
			RoleName: item.Name,
			Outcome:  c.signGame(ctx, accessToken, item.ID, account.GameID),
		})
	}
	return report, nil
}

func (c *Client) refresh(ctx context.Context, account Account) (accessToken, refreshToken, uid string, err error) {
	headers := formHeaders()
	headers.Set("authorization", account.RefreshToken)
	headers.Set("uid", "10000000")
	headers.Set("deviceid", account.DeviceID)
	headers.Set("User-Agent", "okhttp/4.12.0")

	var data struct {
		AccessToken  string    `json:"accessToken"`
		RefreshToken string    `json:"refreshToken"`
		UID          textValue `json:"uid"`
	}
	err = c.call(ctx, http.MethodPost, refreshTokenPath, nil, nil, headers, "刷新令牌", &data)
	if err != nil {
		return "", "", "", err
	}
	if data.AccessToken == "" || data.RefreshToken == "" {
		return "", "", "", fmt.Errorf("刷新令牌返回缺少 accessToken/refreshToken")
	}
	uid = string(data.UID)
	if uid == "" {
		uid = account.UID
	}
	return data.AccessToken, data.RefreshToken, uid, nil
}

func (c *Client) signCommunity(ctx context.Context, accessToken, uid, deviceID string) Outcome {
	headers := formHeaders()
	headers.Set("authorization", accessToken)
	headers.Set("uid", uid)
	headers.Set("deviceid", deviceID)
	headers.Set("User-Agent", "okhttp/4.12.0")
	form := url.Values{"communityId": {"1"}}

	var data struct {
		Exp      textValue `json:"exp"`
		GoldCoin textValue `json:"goldCoin"`
	}
	err := c.call(ctx, http.MethodPost, communitySignPath, nil, form, headers, "社区签到", &data)
	if err == nil {
		return Outcome{
			Success: true,
			Message: "签到成功",
			Reward:  fmt.Sprintf("%s 经验、%s 金币", valueOrZero(data.Exp), valueOrZero(data.GoldCoin)),
		}
	}
	if isAlreadySigned(err) {
		return Outcome{Success: true, AlreadySigned: true, Message: "今日已签到", Reward: "无新增奖励"}
	}
	return Outcome{Message: err.Error()}
}

func (c *Client) gameRoles(ctx context.Context, accessToken, uid, deviceID, gameID string) ([]role, error) {
	headers := make(http.Header)
	headers.Set("authorization", accessToken)
	headers.Set("uid", uid)
	headers.Set("deviceid", deviceID)
	headers.Set("User-Agent", "okhttp/4.12.0")
	query := url.Values{"gameId": {gameID}}

	var data struct {
		Roles []struct {
			RoleID     textValue `json:"roleId"`
			RoleName   string    `json:"roleName"`
			Name       string    `json:"name"`
			ServerName string    `json:"serverName"`
		} `json:"roles"`
	}
	operation := fmt.Sprintf("获取角色列表(gameId=%s)", gameID)
	if err := c.call(ctx, http.MethodGet, gameRolesPath, query, nil, headers, operation, &data); err != nil {
		return nil, err
	}
	roles := make([]role, 0, len(data.Roles))
	seen := make(map[string]struct{}, len(data.Roles))
	for _, item := range data.Roles {
		id := string(item.RoleID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		name := item.RoleName
		if name == "" {
			name = item.Name
		}
		roles = append(roles, role{ID: id, Name: name, ServerName: item.ServerName})
	}
	return roles, nil
}

func (c *Client) signGame(ctx context.Context, accessToken, roleID, gameID string) Outcome {
	headers := formHeaders()
	headers.Set("authorization", accessToken)
	headers.Set("User-Agent", "okhttp/4.12.0")
	form := url.Values{"roleId": {roleID}, "gameId": {gameID}}
	operation := fmt.Sprintf("游戏签到(gameId=%s)", gameID)
	err := c.call(ctx, http.MethodPost, gameSignPath, nil, form, headers, operation, nil)

	outcome := Outcome{Success: true, Message: "签到成功"}
	var state *signState
	if err != nil {
		if !isAlreadySigned(err) {
			return Outcome{Message: err.Error()}
		}
		confirmed, stateErr := c.gameState(ctx, accessToken, gameID)
		if stateErr != nil {
			return Outcome{Message: fmt.Sprintf("%s；状态确认失败：%v", err, stateErr)}
		}
		if !confirmed.TodaySign {
			return Outcome{Message: "接口提示已签到，但签到状态为未签到"}
		}
		outcome.AlreadySigned = true
		outcome.Message = "今日已签到"
		state = &confirmed
	}

	if state == nil {
		confirmed, stateErr := c.gameState(ctx, accessToken, gameID)
		if stateErr != nil {
			outcome.RewardWarning = stateErr.Error()
			return outcome
		}
		state = &confirmed
	}
	reward, rewardErr := c.todayReward(ctx, accessToken, roleID, gameID, *state)
	if rewardErr != nil {
		outcome.RewardWarning = rewardErr.Error()
		return outcome
	}
	outcome.Reward = reward
	return outcome
}

func (c *Client) gameState(ctx context.Context, accessToken, gameID string) (signState, error) {
	headers := make(http.Header)
	headers.Set("authorization", accessToken)
	query := url.Values{"gameId": {gameID}}
	var data struct {
		TodaySign bool      `json:"todaySign"`
		Days      textValue `json:"days"`
	}
	operation := fmt.Sprintf("查询游戏签到状态(gameId=%s)", gameID)
	if err := c.call(ctx, http.MethodGet, gameStatePath, query, nil, headers, operation, &data); err != nil {
		return signState{}, err
	}
	days := 0
	if data.Days != "" {
		parsed, err := strconv.Atoi(string(data.Days))
		if err != nil {
			return signState{}, fmt.Errorf("查询游戏签到状态返回 days 无效")
		}
		days = parsed
	}
	return signState{TodaySign: data.TodaySign, Days: days}, nil
}

func (c *Client) todayReward(ctx context.Context, accessToken, roleID, gameID string, state signState) (string, error) {
	if state.Days <= 0 {
		return "", nil
	}
	headers := make(http.Header)
	headers.Set("authorization", accessToken)
	query := url.Values{"gameId": {gameID}}
	if roleID != "" {
		query.Set("roleId", roleID)
	}
	var raw json.RawMessage
	operation := fmt.Sprintf("查询游戏签到奖励(gameId=%s)", gameID)
	if err := c.call(ctx, http.MethodGet, gameRewardsPath, query, nil, headers, operation, &raw); err != nil {
		return "", err
	}
	items, err := parseRewards(raw)
	if err != nil {
		return "", fmt.Errorf("查询游戏签到奖励返回结构异常")
	}
	if len(items) < state.Days {
		return "", nil
	}
	return items[state.Days-1].String(), nil
}

type rewardItem struct {
	Name     textValue `json:"name"`
	ItemName textValue `json:"itemName"`
	Title    textValue `json:"title"`
	Num      textValue `json:"num"`
	Count    textValue `json:"count"`
	Quantity textValue `json:"quantity"`
}

func (r rewardItem) String() string {
	name := string(r.Name)
	if name == "" {
		name = string(r.ItemName)
	}
	if name == "" {
		name = string(r.Title)
	}
	if name == "" {
		return ""
	}
	count := string(r.Num)
	if count == "" {
		count = string(r.Count)
	}
	if count == "" {
		count = string(r.Quantity)
	}
	if count == "" {
		return name
	}
	return name + "×" + count
}

func parseRewards(raw json.RawMessage) ([]rewardItem, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty rewards")
	}
	if raw[0] == '[' {
		var items []rewardItem
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	var data struct {
		Items   json.RawMessage `json:"items"`
		Rewards json.RawMessage `json:"rewards"`
		List    json.RawMessage `json:"list"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	for _, value := range []json.RawMessage{data.Items, data.Rewards, data.List} {
		if len(value) == 0 {
			continue
		}
		var items []rewardItem
		if err := json.Unmarshal(value, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	return nil, fmt.Errorf("rewards list not found")
}

type envelope struct {
	Code    int             `json:"code"`
	Msg     string          `json:"msg"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	operation string
	message   string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s失败：%s", e.operation, e.message)
}

func (c *Client) call(ctx context.Context, method, path string, query, form url.Values, headers http.Header, operation string, result any) error {
	status, response, err := c.request(ctx, method, path, query, form, headers)
	if status == http.StatusPaymentRequired && operation == "刷新令牌" {
		return &apiError{operation: operation, message: "refreshToken 已失效，请重新登录"}
	}
	if err != nil {
		return fmt.Errorf("%s失败：%w", operation, err)
	}
	if response.Code != 0 {
		return &apiError{operation: operation, message: responseMessage(response)}
	}
	if result == nil || len(response.Data) == 0 || string(response.Data) == "null" {
		return nil
	}
	if err := json.Unmarshal(response.Data, result); err != nil {
		return fmt.Errorf("%s返回数据结构异常", operation)
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, query, form url.Values, headers http.Header) (int, envelope, error) {
	var empty envelope
	requestURL := strings.TrimRight(c.baseURL(), "/") + path
	if len(query) > 0 {
		requestURL += "?" + query.Encode()
	}
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return 0, empty, fmt.Errorf("创建请求失败")
	}
	req.Header = headers.Clone()
	req.Header.Set("appversion", appVersion)
	req.Header.Set("platform", "android")
	ds, err := c.generateDS(appVersion)
	if err != nil {
		return 0, empty, fmt.Errorf("生成 DS 签名失败：%w", err)
	}
	req.Header.Set("ds", ds)
	return c.do(req)
}

func (c *Client) do(req *http.Request) (int, envelope, error) {
	var empty envelope
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return 0, empty, req.Context().Err()
		}
		return 0, empty, fmt.Errorf("网络请求失败，请检查网络后重试")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return resp.StatusCode, empty, fmt.Errorf("读取响应失败")
	}
	if len(data) > maxResponseSize {
		return resp.StatusCode, empty, fmt.Errorf("响应超过大小限制")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return resp.StatusCode, empty, fmt.Errorf("返回空响应（HTTP %d）", resp.StatusCode)
	}
	var response envelope
	if err := json.Unmarshal(data, &response); err != nil {
		return resp.StatusCode, empty, fmt.Errorf("返回非 JSON 或结构异常（HTTP %d）", resp.StatusCode)
	}
	return resp.StatusCode, response, nil
}

func (c *Client) generateDS(version string) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	random := make([]byte, 8)
	if _, err := io.ReadFull(c.random(), random); err != nil {
		return "", err
	}
	nonce := make([]byte, len(random))
	for i, value := range random {
		nonce[i] = alphabet[int(value)%len(alphabet)]
	}
	timestamp := strconv.FormatInt(c.now().Unix(), 10)
	digest := md5.Sum([]byte(timestamp + string(nonce) + version + dsSalt))
	return timestamp + "," + string(nonce) + "," + hex.EncodeToString(digest[:]), nil
}

func formHeaders() http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/x-www-form-urlencoded")
	return headers
}

func responseMessage(response envelope) string {
	message := response.Msg
	if strings.TrimSpace(message) == "" {
		message = response.Message
	}
	message = strings.NewReplacer("\n", " ", "\r", " ").Replace(strings.TrimSpace(message))
	if message == "" {
		return fmt.Sprintf("接口返回错误（code=%d）", response.Code)
	}
	runes := []rune(message)
	if len(runes) > 180 {
		message = string(runes[:180])
	}
	return message
}

func isAlreadySigned(err error) bool {
	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		return false
	}
	return strings.Contains(apiErr.message, "已签到") ||
		strings.Contains(apiErr.message, "签到过") ||
		strings.Contains(apiErr.message, "重复签到")
}

func valueOrZero(value textValue) string {
	if value == "" {
		return "0"
	}
	return string(value)
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return defaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) userBaseURL() string {
	if c.UserBaseURL == "" {
		return defaultUserBaseURL
	}
	return c.UserBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient == nil {
		return NewClient().HTTPClient
	}
	return c.HTTPClient
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *Client) random() io.Reader {
	if c.Random == nil {
		return rand.Reader
	}
	return c.Random
}

type textValue string

func (v *textValue) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*v = ""
		return nil
	}
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*v = textValue(value)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return fmt.Errorf("expected string or number")
	}
	*v = textValue(number.String())
	return nil
}
