package permissions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
)

func (a *Admin) RunDirectoryChecks(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.checkDirectory(ctx)
		}
	}
}

func (a *Admin) checkDirectory(ctx context.Context) {
	a.mu.RLock()
	cfg := a.ldap
	a.mu.RUnlock()
	if cfg == nil {
		return
	}
	state, err := a.platform(ctx)
	if err != nil {
		return
	}
	accounts := make([]platformAccount, 0)
	for _, account := range state.Accounts {
		if account.Kind == "ldap" {
			accounts = append(accounts, account)
		}
	}
	status := map[string]bool{}
	var mu sync.Mutex
	var workers sync.WaitGroup
	slots := make(chan struct{}, 8)
	for chunk := range slices.Chunk(accounts, 10) {
		if ctx.Err() != nil {
			break
		}
		slots <- struct{}{}
		workers.Go(func() {
			defer func() { <-slots }()
			users := map[string]string{}
			for _, account := range chunk {
				users[account.ID] = account.Username
			}
			query, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			result := identity.LDAPDisabled(query, *cfg, users)
			mu.Lock()
			maps.Copy(status, result)
			mu.Unlock()
		})
	}
	workers.Wait()
	if len(status) == 0 || ctx.Err() != nil {
		return
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.ldap != cfg {
		return
	}
	_ = a.editPlatform(ctx, func(current *platformState, tx *sql.Tx) error {
		for id, disabled := range status {
			account := current.Accounts[id]
			if account.ID == "" || account.Generation != state.Accounts[id].Generation || account.DirectoryDisabled == disabled {
				continue
			}
			account.DirectoryDisabled, account.Generation = disabled, rand.Text()
			current.Accounts[id] = account
			if disabled {
				if err := writeOperation(ctx, tx, operation{Actor: "LDAP 状态检查", Action: "目录停用并撤销会话", Risk: "高", Target: account.Username}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
