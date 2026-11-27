package holmes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

// ChatError exposes a diagnostic category without carrying upstream content,
// credentials, or a URL into Portal responses or logs.
type ChatError struct {
	Code           string
	UpstreamStatus int
	Message        string
}

func (e *ChatError) Error() string { return e.Message }
func (*ChatError) Unwrap() error   { return ErrUnavailable }

func chatNetworkError(err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
		return chatTimeout(0)
	}
	return &ChatError{Code: "HOLMES_CHAT_CONNECT_FAILED", Message: "Portal API 未能连接 Holmes 调查服务。请检查 Service 地址、DNS、TLS 和网络策略。"}
}

func chatTimeout(status int) *ChatError {
	return &ChatError{Code: "HOLMES_CHAT_TIMEOUT", UpstreamStatus: status, Message: "Holmes 调查超时。请检查模型服务及上游网关的响应时间后重试。"}
}

func chatHTTPError(status int) *ChatError {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &ChatError{"HOLMES_CHAT_AUTH_FAILED", status, "Holmes 调查接口鉴权失败。请检查 Portal 到 Holmes 的 HTTP 鉴权密钥。"}
	case http.StatusNotFound:
		return &ChatError{"HOLMES_CHAT_ROUTE_NOT_FOUND", status, "Holmes 调查接口未找到。请检查服务根地址及 /api/chat 路由。"}
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return chatTimeout(status)
	case http.StatusTooManyRequests:
		return &ChatError{"HOLMES_CHAT_RATE_LIMITED", status, "Holmes 调查接口触发限流，请稍后重试。"}
	default:
		return &ChatError{"HOLMES_CHAT_UPSTREAM_ERROR", status, fmt.Sprintf("Holmes 调查接口返回 HTTP %d。请查看 Holmes 日志，核对已发布的模型别名和模型服务。", status)}
	}
}
