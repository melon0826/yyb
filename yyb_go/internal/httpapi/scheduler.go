package httpapi

import (
	"context"
	"log"
	"time"

	"yyb_go/internal/store"
)

const (
	renewInterval   = 25 * time.Minute
	renewMaxRetries = 3
	expiredRetry    = 60 * time.Minute // 过期后1小时内仍尝试复活
)

func (a *App) StartScheduler() {
	go a.renewLoop()
}

func (a *App) renewLoop() {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	a.runRenew()
	for range ticker.C {
		a.runRenew()
	}
}

func (a *App) runRenew() {
	ctx, cancel := context.WithTimeout(context.Background(), renewInterval)
	defer cancel()

	accounts, err := a.db.ListAliveAccounts(ctx)
	if err != nil {
		log.Printf("[scheduler] list alive accounts: %v", err)
		return
	}

	// 同时拉取最近过期的账号，尝试复活
	expiredAccounts, expiredErr := a.db.ListRecentlyExpired(ctx, expiredRetry)
	if expiredErr != nil {
		log.Printf("[scheduler] list expired accounts: %v", expiredErr)
	}

	renewed := 0
	failed := 0
	revived := 0

	// 续期存活的账号
	for _, acc := range accounts {
		status := a.renewAccount(ctx, acc)
		if status == "alive" {
			renewed++
		} else {
			failed++
		}
	}

	// 尝试复活最近过期的账号
	for _, acc := range expiredAccounts {
		status := a.renewAccount(ctx, acc)
		if status == "alive" {
			revived++
		}
	}

	total := len(accounts)
	if total > 0 {
		log.Printf("[scheduler] renewed %d/%d alive (%d failed)",
			renewed, total, failed)
	}
	if len(expiredAccounts) > 0 {
		log.Printf("[scheduler] revived %d/%d recently-expired accounts",
			revived, len(expiredAccounts))
	}
}

func (a *App) renewAccount(ctx context.Context, acc *store.WechatAccount) string {
	lastStatus := "alive"
	for attempt := 0; attempt < renewMaxRetries; attempt++ {
		status := a.refreshLiveness(ctx, acc)
		if status == "alive" {
			return "alive"
		}
		lastStatus = status
		if attempt < renewMaxRetries-1 {
			time.Sleep(5 * time.Second)
		}
	}
	if lastStatus != "alive" {
		_ = a.db.InsertAuditLog(context.Background(), "auto_renew_failed", acc.OpenID, lastStatus, "")
	}
	return lastStatus
}
