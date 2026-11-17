package security

import (
	"fmt"
	"strings"
)

type Mode string

const (
	ModeDevelopment Mode = "development"
	ModeProduction  Mode = "production"
)

func ParseMode(value string) (Mode, error) {
	normalized := Mode(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case ModeDevelopment, ModeProduction:
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported security mode %q", value)
	}
}
