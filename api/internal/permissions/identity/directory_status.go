package identity

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// LDAPDisabled checks a group of known principals over a single service bind.
// Uncertain responses are omitted to preserve active sessions until expiry.
func LDAPDisabled(ctx context.Context, cfg LDAPConfig, users map[string]string) map[string]bool {
	results := make(map[string]bool)
	conn, closeConnection, stage := dialLDAP(ctx, cfg)
	if stage != "" {
		return results
	}
	defer closeConnection()
	if err := conn.Bind(cfg.BindDN, cfg.BindPassword); err != nil {
		return results
	}
	for subject, username := range users {
		if ctx.Err() != nil {
			break
		}
		entry, stage := cfg.searchDirectoryUser(conn, username, []string{"userAccountControl", "pwdAccountLockedTime", "nsAccountLock", "shadowExpire"})
		if stage == "user_absent" {
			results[subject] = true
			continue
		}
		if stage != "success" {
			continue
		}
		if "ldap:"+strings.ToLower(entry.DN) != subject {
			results[subject] = true
			continue
		}
		results[subject] = directoryEntryDisabled(entry, time.Now())
	}
	return results
}

func directoryEntryDisabled(entry *ldap.Entry, now time.Time) bool {
	uac, _ := strconv.Atoi(entry.GetEqualFoldAttributeValue("userAccountControl"))
	expires, err := strconv.ParseInt(entry.GetEqualFoldAttributeValue("shadowExpire"), 10, 64)
	expired := err == nil && expires >= 0 && expires <= now.Unix()/86400
	return uac&2 != 0 || strings.EqualFold(entry.GetEqualFoldAttributeValue("nsAccountLock"), "true") ||
		entry.GetEqualFoldAttributeValue("pwdAccountLockedTime") == "000001010000Z" || expired
}
