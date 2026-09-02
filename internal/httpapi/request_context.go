package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const (
	RequestIDHeader     = "X-Request-ID"
	CorrelationIDHeader = "X-Correlation-ID"
	maxRequestIDLength  = 128
)

type requestMetadata struct {
	RequestID     string
	CorrelationID string
}

type requestMetadataContextKey struct{}

var fallbackRequestIDCounter atomic.Uint64

func requestMetadataMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(RequestIDHeader))
		if !validRequestMetadataID(requestID) {
			requestID = newRequestMetadataID("req_")
		}

		correlationID := strings.TrimSpace(r.Header.Get(CorrelationIDHeader))
		if !validRequestMetadataID(correlationID) {
			correlationID = requestID
		}

		metadata := requestMetadata{RequestID: requestID, CorrelationID: correlationID}
		ctx := context.WithValue(r.Context(), requestMetadataContextKey{}, metadata)

		w.Header().Set(RequestIDHeader, requestID)
		w.Header().Set(CorrelationIDHeader, correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestMetadataFromContext(ctx context.Context) requestMetadata {
	metadata, _ := ctx.Value(requestMetadataContextKey{}).(requestMetadata)
	return metadata
}

// RequestIDFromContext returns the validated service request identifier.
func RequestIDFromContext(ctx context.Context) string {
	return requestMetadataFromContext(ctx).RequestID
}

// CorrelationIDFromContext returns the validated correlation identifier.
func CorrelationIDFromContext(ctx context.Context) string {
	return requestMetadataFromContext(ctx).CorrelationID
}

func validRequestMetadataID(value string) bool {
	if value == "" || len(value) > maxRequestIDLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == ':', r == '/':
		default:
			return false
		}
	}
	return true
}

func newRequestMetadataID(prefix string) string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return prefix + hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%s%x-%x", prefix, time.Now().UnixNano(), fallbackRequestIDCounter.Add(1))
}
