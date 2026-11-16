package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/abchw517/kube-aiops/internal/identitystore"
)

type storedLDAPSettings struct {
	ldapSettings
	PasswordCiphertext string `json:"passwordCiphertext,omitempty"`
}

func ldapPasswordCipher() (cipher.AEAD, error) {
	path := os.Getenv("AUTH_LDAP_ENCRYPTION_KEY_FILE")
	if path == "" {
		return nil, errors.New("请挂载 LDAP 密码加密密钥并设置 AUTH_LDAP_ENCRYPTION_KEY_FILE")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("无法读取 LDAP 密码加密密钥")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("LDAP 密码加密密钥必须是 Base64 编码的 32 字节随机值")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func ldapPasswordContext(settings ldapSettings) []byte {
	context, _ := json.Marshal([3]string{"kube-aiops/ldap-password/v1", settings.URL, settings.BindDN})
	return context
}

func encodeLDAPSettings(settings ldapSettings) ([]byte, error) {
	document := storedLDAPSettings{ldapSettings: settings}
	document.BindPassword = ""
	if settings.BindPassword != "" {
		aead, err := ldapPasswordCipher()
		if err != nil {
			return nil, err
		}
		encrypted := aead.Seal(nil, nil, []byte(settings.BindPassword), ldapPasswordContext(settings))
		document.PasswordCiphertext = base64.StdEncoding.EncodeToString(encrypted)
	}
	return json.Marshal(document)
}

func loadLDAPSettings(ctx context.Context, store *identitystore.Store, raw []byte) (ldapSettings, error) {
	var document storedLDAPSettings
	if err := json.Unmarshal(raw, &document); err != nil {
		return ldapSettings{}, errors.New("无法读取已保存的 LDAP 配置，请重新保存配置")
	}
	settings := document.ldapSettings
	if document.PasswordCiphertext != "" {
		aead, err := ldapPasswordCipher()
		if err != nil {
			return settings, err
		}
		encrypted, err := base64.StdEncoding.DecodeString(document.PasswordCiphertext)
		if err != nil {
			return settings, errors.New("LDAP 搜索密码密文无效，请重新保存搜索密码")
		}
		password, err := aead.Open(nil, nil, encrypted, ldapPasswordContext(settings))
		if err != nil {
			return settings, errors.New("无法解密 LDAP 搜索密码，请检查密钥或重新保存搜索密码")
		}
		settings.BindPassword = string(password)
	} else if settings.BindPassword != "" {
		// Keep the old database format readable during an upgrade and migrate it
		// before LDAP can use the credential.
		updated, err := encodeLDAPSettings(settings)
		if err != nil {
			settings.BindPassword = ""
			return settings, err
		}
		result, err := store.ExecContext(ctx, "UPDATE ldap_settings SET document=? WHERE id=1 AND document=?", string(updated), string(raw))
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count == 1 {
				return settings, nil
			}
		}
		settings.BindPassword = ""
		return settings, errors.New("无法迁移 LDAP 搜索密码，请重启 API 或重新保存配置")
	}
	return settings, nil
}
