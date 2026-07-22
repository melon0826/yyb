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
	if len(accounts) == 0 {
		return
	}

	renewed := 0
	failed := 0
	for _, acc := range accounts {
		status := a.renewAccount(ctx, acc)
		if status == "alive" {
			renewed++
		} else {
			failed++
		}
	}
	if renewed+failed > 0 {
		log.Printf("[scheduler] renewed %d/%d alive accounts (%d failed)",
			renewed, len(accounts), failed)
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
