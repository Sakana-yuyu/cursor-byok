// fetch-native-prompt 验证并同步 Cursor 云端提示词（A2 协议验证 + A3 拉取）。
//
// 前置：已在 cursor-byok 中登录官方 Cursor 账号。
// 用法：
//
//	go run ./cmd/fetch-native-prompt            # 验证模式：打印各端点结果
//	go run ./cmd/fetch-native-prompt --sync     # 同步模式：成功结果落盘到 appdata/native-prompts/
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"cursor/internal/backend/promptsync"
)

func main() {
	syncMode := flag.Bool("sync", false, "把成功拉取的提示词落盘到 appdata/native-prompts/")
	showAll := flag.Bool("all", false, "打印完整提示词而不是前 2000 字符")
	flag.Parse()

	ctx := context.Background()
	result, err := promptsync.Fetch(ctx, "agent")
	if err != nil {
		fmt.Printf("拉取失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("来源: %s\n", result.Source)
	if result.TokenCount > 0 {
		fmt.Printf("token_count: %d\n", result.TokenCount)
	}
	fmt.Printf("长度: %d 字符\n", len(result.Content))
	if *showAll {
		fmt.Println("---- 完整内容 ----")
		fmt.Println(result.Content)
	} else {
		limit := 2000
		if len(result.Content) > limit {
			fmt.Printf("---- 前 %d 字符 ----\n", limit)
			fmt.Println(result.Content[:limit])
			fmt.Println("...")
		} else {
			fmt.Println("---- 内容 ----")
			fmt.Println(result.Content)
		}
	}

	if *syncMode {
		if err := promptsync.Save("agent", *result); err != nil {
			fmt.Printf("落盘失败: %v\n", err)
			os.Exit(1)
		}
		path, _ := promptsync.CachePath("agent")
		fmt.Printf("已缓存到: %s\n", path)
	}
}