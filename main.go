package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	cfg := loadConfig()
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Printf("WARN: 加载时区 %s 失败，使用 Local: %v", cfg.Timezone, err)
		loc = time.Local
	}

	store := NewStorage(cfg.DataDir)
	if err := store.Init(); err != nil {
		log.Fatalf("初始化数据目录失败: %v", err)
	}

	hostname, _ := os.Hostname()
	log.Printf("devops-worker starting: pid=%d hostname=%s data_dir=%s web_addr=%s", os.Getpid(), hostname, cfg.DataDir, cfg.WebAddr)

	tg, err := NewTelegramService(cfg, store, loc)
	if err != nil {
		log.Fatalf("Telegram 初始化失败: %v", err)
	}

	app := &App{Cfg: cfg, Store: store, Loc: loc, TG: tg}
	if tg != nil {
		tg.StartPollingAndScheduler()
	}

	log.Printf("Web server listening on %s", cfg.WebAddr)
	if err := http.ListenAndServe(cfg.WebAddr, app.routes()); err != nil {
		log.Fatalf("Web server stopped: %v", err)
	}
}
