package nte

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	sendCodePath        = "/m/newApi/sendPhoneCaptchaWithOutLogin"
	checkCodePath       = "/m/newApi/checkPhoneCaptchaWithOutLogin"
	sdkLoginPath        = "/openApi/sms/new/login"
	userCenterLoginPath = "/usercenter/api/login"

	loginSecret     = "89155cc4e8634ec5b1b6364013b23e3e"
	loginAppID      = "10550"
	userCenterAppID = "10551"
)

var (
	phonePattern = regexp.MustCompile(`^1\d{10}$`)
	codePattern  = regexp.MustCompile(`^\d{4,8}$`)
)

func (c *Client) SendLoginCode(ctx context.Context, phone, deviceID string) error {
	if !phonePattern.MatchString(phone) {
		return fmt.Errorf("手机号格式不正确")
	}
	if deviceID == "" {
		return fmt.Errorf("deviceId 不能为空")
	}
	form := url.Values{
		"deviceType":  {"LGE-AN10"},
		"type":        {"16"},
		"deviceId":    {deviceID},
		"deviceName":  {"LGE-AN10"},
		"versionCode": {"1"},
		"t":           {strconv.FormatInt(c.now().Unix(), 10)},
		"areaCodeId":  {"1"},
		"appId":       {loginAppID},
		"deviceSys":   {"12"},
		"cellphone":   {phone},
		"deviceModel": {"LGE-AN10"},
		"sdkVersion":  {"4.129.0"},
		"bid":         {"com.pwrd.htassistant"},
		"channelId":   {"1"},
	}
	signLoginParams(form)
	return c.callUser(ctx, sendCodePath, form, "发送验证码", nil)
}

func (c *Client) LoginBySMS(ctx context.Context, phone, code, deviceID string) (Account, error) {
	var account Account
	if !phonePattern.MatchString(phone) {
		return account, fmt.Errorf("手机号格式不正确")
	}
	if !codePattern.MatchString(code) {
		return account, fmt.Errorf("验证码应为 4 至 8 位数字")
	}
	if deviceID == "" {
		return account, fmt.Errorf("deviceId 不能为空")
	}
	if err := c.checkLoginCode(ctx, phone, code, deviceID); err != nil {
		return account, err
	}
	token, userID, err := c.sdkLogin(ctx, phone, code, deviceID)
	if err != nil {
		return account, err
	}
	return c.userCenterLogin(ctx, token, userID, deviceID)
}

func (c *Client) checkLoginCode(ctx context.Context, phone, code, deviceID string) error {
	form := url.Values{
		"deviceType":  {"LGE-AN10"},
		"deviceId":    {deviceID},
		"deviceName":  {"LGE-AN10"},
		"versionCode": {"1"},
		"t":           {strconv.FormatInt(c.now().Unix(), 10)},
		"captcha":     {code},
		"appId":       {loginAppID},
		"deviceSys":   {"12"},
		"cellphone":   {phone},
		"deviceModel": {"LGE-AN10"},
		"sdkVersion":  {"4.129.0"},
		"bid":         {"com.pwrd.htassistant"},
		"channelId":   {"1"},
	}
	signLoginParams(form)
	return c.callUser(ctx, checkCodePath, form, "校验验证码", nil)
}

func (c *Client) sdkLogin(ctx context.Context, phone, code, deviceID string) (string, string, error) {
	encryptedPhone, err := encryptLoginValue(phone)
	if err != nil {
		return "", "", err
	}
	encryptedCode, err := encryptLoginValue(code)
	if err != nil {
		return "", "", err
	}
	form := url.Values{
		"deviceType":  {"LGE-AN10"},
		"idfa":        {""},
		"sign":        {""},
		"adm":         {""},
		"type":        {"16"},
		"deviceId":    {deviceID},
		"version":     {"1"},
		"deviceName":  {"LGE-AN10"},
		"mac":         {""},
		"t":           {strconv.FormatInt(c.now().UnixMilli(), 10)},
		"areaCodeId":  {"1"},
		"captcha":     {encryptedCode},
		"appId":       {loginAppID},
		"deviceSys":   {"12"},
		"cellphone":   {encryptedPhone},
		"deviceModel": {"LGE-AN10"},
		"sdkVersion":  {"4.129.0"},
		"bid":         {"com.pwrd.htassistant"},
		"channelId":   {"1"},
	}
	signLoginParams(form)
	var result struct {
		Token  string    `json:"token"`
		UserID textValue `json:"userId"`
	}
	if err := c.callUser(ctx, sdkLoginPath, form, "短信登录", &result); err != nil {
		return "", "", err
	}
	if result.Token == "" || result.UserID == "" {
		return "", "", fmt.Errorf("短信登录返回缺少 token/userId")
	}
	return result.Token, string(result.UserID), nil
}

func (c *Client) userCenterLogin(ctx context.Context, token, userID, deviceID string) (Account, error) {
	headers := formHeaders()
	headers.Set("authorization", "")
	headers.Set("uid", "10000000")
	headers.Set("deviceid", deviceID)
	headers.Set("User-Agent", "okhttp/4.12.0")
	form := url.Values{
		"token":        {token},
		"userIdentity": {userID},
		"appId":        {userCenterAppID},
	}
	var data struct {
		AccessToken  string    `json:"accessToken"`
		RefreshToken string    `json:"refreshToken"`
		UID          textValue `json:"uid"`
	}
	if err := c.call(ctx, http.MethodPost, userCenterLoginPath, nil, form, headers, "用户中心登录", &data); err != nil {
		return Account{}, err
	}
	if data.AccessToken == "" || data.RefreshToken == "" {
		return Account{}, fmt.Errorf("用户中心登录返回缺少 accessToken/refreshToken")
	}
	return Account{
		RefreshToken: data.RefreshToken,
		UID:          string(data.UID),
		DeviceID:     deviceID,
		GameID:       DefaultGameID,
	}, nil
}

func (c *Client) callUser(ctx context.Context, path string, form url.Values, operation string, result any) error {
	requestURL := strings.TrimRight(c.userBaseURL(), "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("%s失败：创建请求失败", operation)
	}
	req.Header = formHeaders()
	req.Header.Set("platform", "android")
	_, response, err := c.do(req)
	if err != nil {
		return fmt.Errorf("%s失败：%w", operation, err)
	}
	if response.Code != 0 {
		return &apiError{operation: operation, message: responseMessage(response)}
	}
	if result == nil {
		return nil
	}
	if len(response.Result) == 0 || string(response.Result) == "null" {
		return fmt.Errorf("%s返回缺少 result", operation)
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("%s返回数据结构异常", operation)
	}
	return nil
}

func signLoginParams(params url.Values) {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var content strings.Builder
	for _, key := range keys {
		content.WriteString(params.Get(key))
	}
	digest := md5.Sum([]byte(content.String() + loginSecret))
	params.Set("sign", hex.EncodeToString(digest[:]))
}

// 该 AES-ECB 实现仅用于兼容塔吉多现有登录协议，不用于新项目的数据加密。
func encryptLoginValue(value string) (string, error) {
	key := []byte(loginSecret[len(loginSecret)-16:])
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化登录加密失败：%w", err)
	}
	plain := []byte(value)
	padding := block.BlockSize() - len(plain)%block.BlockSize()
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(plain))
	for offset := 0; offset < len(plain); offset += block.BlockSize() {
		block.Encrypt(encrypted[offset:offset+block.BlockSize()], plain[offset:offset+block.BlockSize()])
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}
