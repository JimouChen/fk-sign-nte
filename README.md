# fk-sign-nte

异环·塔吉多单账户每日签到工具，使用 Go 编写。支持社区签到、自动获取游戏角色、游戏签到、重复签到识别，以及当日奖励说明。

## 签到结果

程序会在终端和 GitHub Actions 页面摘要中输出结果，例如：

```text
社区签到成功｜奖励：10 经验、20 金币
角色 测试角色(role-1)签到成功｜今日道具：稀有核心×3
```

“今日已签到”也视为成功，但不会把昨日或其他日期的道具当作本次新增奖励。签到成功而奖励查询失败时，会单独显示奖励说明获取失败。

## 配置

| 环境变量 | 必填 | 说明 |
| --- | --- | --- |
| `NTE_REFRESH_TOKEN` | 是 | 塔吉多 `refreshToken`，可通过下方本地登录命令获取 |
| `NTE_UID` | 建议 | 用户 UID；刷新接口未返回 UID 时必填 |
| `NTE_DEVICE_ID` | 建议 | 固定设备 ID；不配置时每次运行随机生成 |
| `NTE_GAME_ID` | 否 | 游戏 ID，默认 `1289` |
| `NTE_ROLE_ID` | 否 | 角色列表不可用或为空时的备用角色 ID |
| `NTE_REPO_TOKEN` | 否 | 用于自动回写轮换后的 `NTE_REFRESH_TOKEN`，见下文 |

不要把真实令牌写入代码、README 或提交到仓库。

## 本地获取 refreshToken

需要 Go 1.25 或更新版本。直接在当前项目运行：

```bash
go run ./cmd/nte-login
```

按终端提示输入手机号和短信验证码。登录成功后，终端会显示 `NTE_REFRESH_TOKEN`、`NTE_UID` 和 `NTE_DEVICE_ID`。这些值只会显示在本机终端，不会写入项目文件；请将它们保存到 GitHub 仓库 Secrets，不要发送到聊天中。

### 手机重新登录后需要更新 Token

如果你在手机上使用手机号和验证码重新登录塔吉多，需要重新执行 `go run ./cmd/nte-login` 获取最新的 `NTE_REFRESH_TOKEN`，并将它和新的 `NTE_DEVICE_ID` 更新到 GitHub 仓库 Secrets，否则后续 Actions 可能因旧令牌失效而执行失败。`NTE_REPO_TOKEN` 无法自动同步手机登录产生的新令牌。

## 本地运行签到

```bash
export NTE_REFRESH_TOKEN='你的 refreshToken'
export NTE_UID='你的 UID'
export NTE_DEVICE_ID='固定设备 ID'
go run ./cmd/nte-sign
```

运行测试与构建：

```bash
go test ./...
go build -trimpath -o bin/nte-sign ./cmd/nte-sign
```

## GitHub Actions 每日签到

工作流位于 `.github/workflows/daily-sign.yml`，每天北京时间 **00:17** 执行，也可在 Actions 页面手动触发。GitHub 定时任务可能稍有延迟。

1. 打开仓库 **Settings → Secrets and variables → Actions**。
2. 添加 `NTE_REFRESH_TOKEN`，并建议添加 `NTE_UID`、`NTE_DEVICE_ID`。
3. 按需添加 `NTE_GAME_ID`、`NTE_ROLE_ID`。
4. 建议按下节配置 `NTE_REPO_TOKEN`，以支持刷新令牌轮换。
5. 在 Actions 页面手动运行一次“每日签到”，检查日志和页面摘要中的奖励结果。

### 自动更新轮换后的 refreshToken

签到前刷新令牌时，接口可能返回新的 `refreshToken`。Actions 的临时文件无法可靠保存到次日，因此工具支持通过 GitHub API 自动更新仓库 Secret：

1. 创建一个仅授权当前仓库的 fine-grained personal access token。
2. 在该仓库的 **Repository permissions** 中添加 **Secret**（部分文档称 **Secrets**），设置为 **Read and write**，无需添加其他权限。
3. 将令牌保存为仓库 Secret `NTE_REPO_TOKEN`。

之后，只有接口返回了不同的 `refreshToken`，程序才会使用 NaCl 密封盒加密并更新 `NTE_REFRESH_TOKEN`。令牌值不会写入日志或 Actions 摘要。

如果不配置 `NTE_REPO_TOKEN`，签到仍可执行；但检测到令牌轮换时只输出提示，需要手动更新仓库 Secret，否则后续运行可能因旧令牌失效而失败。

## 实现说明

核心协议逻辑参考本地 `astrbot_plugin_nte`：短信登录、刷新令牌、生成 DS 签名、社区签到、获取角色、游戏签到、查询签到状态和奖励。已移除 AstrBot、多账户管理及云异环时长领取，仅保留独立的本地短信登录命令和单账户每日签到链路。
