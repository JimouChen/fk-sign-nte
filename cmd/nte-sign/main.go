package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JimouChen/fk-sign-nte/internal/ghsecret"
	"github.com/JimouChen/fk-sign-nte/internal/nte"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "签到任务失败：%v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	account := nte.Account{
		RefreshToken: strings.TrimSpace(os.Getenv("NTE_REFRESH_TOKEN")),
		UID:          strings.TrimSpace(os.Getenv("NTE_UID")),
		DeviceID:     strings.TrimSpace(os.Getenv("NTE_DEVICE_ID")),
		GameID:       strings.TrimSpace(os.Getenv("NTE_GAME_ID")),
		RoleID:       strings.TrimSpace(os.Getenv("NTE_ROLE_ID")),
	}
	if account.RefreshToken == "" {
		return fmt.Errorf("请设置 NTE_REFRESH_TOKEN")
	}
	if account.DeviceID == "" {
		deviceID, err := nte.NewDeviceID()
		if err != nil {
			return err
		}
		account.DeviceID = deviceID
	}

	report, signErr := nte.NewClient().Sign(ctx, account)
	lines := reportLines(report, signErr)
	fmt.Println("异环签到结果")
	for _, line := range lines {
		fmt.Println(line)
	}

	var errs []error
	if signErr != nil {
		errs = append(errs, signErr)
	} else if !report.OK() {
		errs = append(errs, fmt.Errorf("部分签到失败"))
	}
	if err := writeStepSummary(lines); err != nil {
		errs = append(errs, err)
	}
	if report.NewRefreshToken != "" {
		if err := syncRefreshToken(ctx, report.NewRefreshToken); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func reportLines(report nte.Report, signErr error) []string {
	lines := make([]string, 0, len(report.Roles)+3)
	if report.UID != "" {
		lines = append(lines, "账号 UID："+report.UID)
	}
	if report.Community.Message != "" {
		lines = append(lines, outcomeLine("社区", report.Community, "奖励"))
	}
	for _, role := range report.Roles {
		label := "角色 " + role.RoleID
		if role.RoleName != "" {
			label = fmt.Sprintf("角色 %s(%s)", role.RoleName, role.RoleID)
		}
		lines = append(lines, outcomeLine(label, role.Outcome, "今日道具"))
	}
	if signErr != nil {
		lines = append(lines, "错误："+signErr.Error())
	}
	if len(lines) == 0 {
		lines = append(lines, "未产生签到结果")
	}
	return lines
}

func outcomeLine(label string, outcome nte.Outcome, rewardLabel string) string {
	line := label + outcome.Message
	if outcome.Reward != "" {
		line += "｜" + rewardLabel + "：" + outcome.Reward
	} else if outcome.Success && rewardLabel == "今日道具" && outcome.RewardWarning == "" {
		line += "｜" + rewardLabel + "：未返回"
	}
	if outcome.RewardWarning != "" {
		line += "｜奖励说明获取失败：" + outcome.RewardWarning
	}
	return line
}

func writeStepSummary(lines []string) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("打开 Actions 摘要失败：%w", err)
	}
	defer file.Close()
	content := "# 异环每日签到\n\n" + strings.Join(lines, "  \n") + "\n"
	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("写入 Actions 摘要失败：%w", err)
	}
	return nil
}

func syncRefreshToken(ctx context.Context, refreshToken string) error {
	repoToken := strings.TrimSpace(os.Getenv("NTE_REPO_TOKEN"))
	if repoToken == "" {
		fmt.Println("提示：refreshToken 已轮换；配置 NTE_REPO_TOKEN 后可自动更新仓库 Secret。")
		return nil
	}
	repository := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY"))
	if repository == "" {
		return fmt.Errorf("refreshToken 已轮换，但缺少 GITHUB_REPOSITORY，无法更新 Secret")
	}
	if err := ghsecret.Update(ctx, nil, "https://api.github.com", repository, repoToken, "NTE_REFRESH_TOKEN", refreshToken); err != nil {
		return err
	}
	fmt.Println("已安全更新仓库 Secret NTE_REFRESH_TOKEN。")
	return nil
}
