package main

import (
	"log"
	"os"
	"strconv"
	"strings"
)

func loadConfig() Config {
	cfg := Config{
		BotToken:        strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		GroupTopicMap:   map[int64]int{},
		WebAddr:         envOr("WEB_ADDR", ":8080"),
		DataDir:         envOr("DATA_DIR", "./data"),
		Timezone:        envOr("APP_TIMEZONE", "Asia/Shanghai"),
		AdminUsername:   envOr("ADMIN_USERNAME", "admin"),
		AdminPassword:   envOr("ADMIN_PASSWORD", "change_me"),
		GroupChatIDs:    parseInt64List(os.Getenv("GROUP_CHAT_IDS")),
		ApproverUserIDs: parseInt64List(os.Getenv("APPROVER_USER_IDS")),
	}

	if topicStr := strings.TrimSpace(os.Getenv("GROUP_CHAT_TOPICS")); topicStr != "" {
		for _, p := range strings.Split(topicStr, ",") {
			parts := strings.Split(strings.TrimSpace(p), ":")
			if len(parts) != 2 {
				continue
			}
			chatID, err1 := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
			topicID, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 == nil && err2 == nil {
				cfg.GroupTopicMap[chatID] = topicID
			}
		}
	}

	if cfg.BotToken == "" {
		log.Printf("WARN: BOT_TOKEN 未设置，Telegram 通知、审批回调、提醒功能将不可用")
	}
	if len(cfg.ApproverUserIDs) == 0 {
		log.Printf("WARN: APPROVER_USER_IDS 未设置，审批按钮将无法通过权限校验")
	}
	return cfg
}

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func parseInt64List(s string) []int64 {
	var out []int64
	for _, raw := range strings.Split(s, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			log.Printf("WARN: 无法解析 int64 配置 %q: %v", raw, err)
			continue
		}
		out = append(out, v)
	}
	return out
}
