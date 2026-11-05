package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JimouChen/fk-sign-nte/internal/nte"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "登录失败：%v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, input io.Reader) error {
	reader := bufio.NewReader(input)
	fmt.Println("塔吉多短信登录（手机号和验证码仅在本机终端使用）")
	phone, err := readLine(reader, "手机号：")
	if err != nil {
		return err
	}
	deviceID, err := nte.NewDeviceID()
	if err != nil {
		return err
	}
	client := nte.NewClient()
	if err := client.SendLoginCode(ctx, phone, deviceID); err != nil {
		return err
	}
	fmt.Println("验证码已发送，请查收短信。")
	code, err := readLine(reader, "短信验证码：")
	if err != nil {
		return err
	}
	account, err := client.LoginBySMS(ctx, phone, code, deviceID)
	if err != nil {
		return err
	}
	if account.UID == "" {
		account.UID, err = readLine(reader, "接口未返回 UID，请输入已知 UID：")
		if err != nil {
			return err
		}
	}

	fmt.Println("\n登录成功。请将以下值保存到 GitHub 仓库 Secrets，不要分享或提交到代码仓库：")
	fmt.Printf("NTE_REFRESH_TOKEN=%s\n", account.RefreshToken)
	fmt.Printf("NTE_UID=%s\n", account.UID)
	fmt.Printf("NTE_DEVICE_ID=%s\n", account.DeviceID)
	return nil
}

func readLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	value, err := reader.ReadString('\n')
	if err != nil && len(value) == 0 {
		return "", fmt.Errorf("读取输入失败：%w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("输入不能为空")
	}
	return value, nil
}
