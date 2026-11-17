package finding

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

const cursorVersion = 1

type Cursor struct {
	Version   int    `json:"v"`
	CreatedAt string `json:"createdAt"`
	ID        string `json:"id"`
}

func EncodeCursor(item Finding) string {
	if strings.TrimSpace(item.ID) == "" {
		return ""
	}
	payload, _ := json.Marshal(Cursor{
		Version:   cursorVersion,
		CreatedAt: item.CreatedAt,
		ID:        item.ID,
	})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeCursor(token string) (*Cursor, error) {
	if strings.TrimSpace(token) == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var cursor Cursor
	if err := json.Unmarshal(decoded, &cursor); err != nil ||
		cursor.Version != cursorVersion || strings.TrimSpace(cursor.ID) == "" {
		return nil, ErrInvalidCursor
	}
	return &cursor, nil
}
