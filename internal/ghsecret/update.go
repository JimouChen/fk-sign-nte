// Package ghsecret updates GitHub Actions secrets without exposing their values.
package ghsecret

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/box"
)

const maxResponseSize = 1 << 20

// Update seals value with the repository's public key and updates secretName.
func Update(ctx context.Context, client *http.Client, apiURL, repository, token, secretName, value string) error {
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return fmt.Errorf("GITHUB_REPOSITORY 格式应为 owner/repo")
	}
	if token == "" {
		return fmt.Errorf("缺少仓库访问令牌")
	}
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	endpoint := strings.TrimRight(apiURL, "/") + "/repos/" + owner + "/" + name + "/actions/secrets/"
	var publicKey struct {
		KeyID string `json:"key_id"`
		Key   string `json:"key"`
	}
	if err := request(ctx, client, http.MethodGet, endpoint+"public-key", token, nil, &publicKey); err != nil {
		return fmt.Errorf("获取仓库公钥失败：%w", err)
	}
	keyBytes, err := base64.StdEncoding.DecodeString(publicKey.Key)
	if err != nil || len(keyBytes) != 32 || publicKey.KeyID == "" {
		return fmt.Errorf("仓库公钥格式无效")
	}
	var key [32]byte
	copy(key[:], keyBytes)
	sealed, err := box.SealAnonymous(nil, []byte(value), &key, rand.Reader)
	if err != nil {
		return fmt.Errorf("加密 refreshToken 失败：%w", err)
	}

	payload, err := json.Marshal(map[string]string{
		"encrypted_value": base64.StdEncoding.EncodeToString(sealed),
		"key_id":          publicKey.KeyID,
	})
	if err != nil {
		return fmt.Errorf("构造 Secret 请求失败：%w", err)
	}
	if err := request(ctx, client, http.MethodPut, endpoint+secretName, token, payload, nil); err != nil {
		return fmt.Errorf("更新仓库 Secret 失败：%w", err)
	}
	return nil
}

func request(ctx context.Context, client *http.Client, method, url, token string, body []byte, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建请求失败")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "fk-sign-nte")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("网络请求失败")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil || len(data) > maxResponseSize {
		return fmt.Errorf("读取响应失败")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if result != nil && len(data) > 0 {
		if err := json.Unmarshal(data, result); err != nil {
			return fmt.Errorf("响应结构异常")
		}
	}
	return nil
}
