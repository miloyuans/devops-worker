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
	bootstrapSSOSettingsFromEnv(store, cfg)
	if summary, err := store.RepairActiveSchedule(loc); err != nil {
		log.Printf("repair active schedule failed: %v", err)
	} else if summary.ChangedItems > 0 {
		log.Printf("repair active schedule removed %d duplicate items, revision=%d version=%s", summary.ChangedItems, summary.NewRevision, summary.VersionID)
	}

	hostname, _ := os.Hostname()
	log.Printf("devops-worker starting: pid=%d hostname=%s data_dir=%s web_addr=%s", os.Getpid(), hostname, cfg.DataDir, cfg.WebAddr)

	tg, err := NewTelegramService(cfg, store, loc)
	if err != nil {
		log.Fatalf("Telegram 初始化失败: %v", err)
	}

	app := &App{Cfg: cfg, Store: store, Loc: loc, TG: tg}
	go startScheduleConsistencyScheduler(store, loc, tg)
	if tg != nil {
		tg.StartPollingAndScheduler()
	}

	log.Printf("Web server listening on %s", cfg.WebAddr)
	if err := http.ListenAndServe(cfg.WebAddr, app.routes()); err != nil {
		log.Fatalf("Web server stopped: %v", err)
	}
}

func startScheduleConsistencyScheduler(store *Storage, loc *time.Location, tg *TelegramService) {
	if store == nil {
		return
	}
	run := func() {
		summary, err := store.SyncActiveItemsWithLatestShifts(loc)
		if err != nil {
			log.Printf("schedule consistency sync failed: %v", err)
			return
		}
		if summary.ChangedItems > 0 {
			log.Printf("schedule consistency sync updated %d active items, revision=%d version=%s", summary.ChangedItems, summary.NewRevision, summary.VersionID)
			if tg != nil {
				tg.WakeNotificationQueue()
			}
		}
	}
	run()
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		run()
	}
}

func bootstrapSSOSettingsFromEnv(store *Storage, cfg Config) {
	if store == nil || !cfg.SSOEnabled {
		return
	}
	settings, err := store.LoadSSOSettings()
	if err != nil {
		log.Printf("load sso settings for bootstrap failed: %v", err)
		return
	}
	if settings.Enabled || settings.IssuerURL != "" || settings.ClientID != "" || settings.RedirectURL != "" {
		return
	}
	settings.Enabled = true
	settings.IssuerURL = cfg.OIDCIssuerURL
	settings.ClientID = cfg.OIDCClientID
	settings.ClientSecret = cfg.OIDCClientSecret
	settings.RedirectURL = cfg.OIDCRedirectURL
	settings.Scopes = cfg.OIDCScopes
	settings.AdminUsers = cfg.SSOAdminUsers
	settings.AdminRoles = cfg.SSOAdminRoles
	settings.UserRoles = []string{"devops-worker-user", "user"}
	if err := store.SaveSSOSettings(settings); err != nil {
		log.Printf("bootstrap sso settings from env failed: %v", err)
	}
}
