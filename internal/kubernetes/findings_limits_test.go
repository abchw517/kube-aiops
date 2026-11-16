package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestResultsPaginationBounds(t *testing.T) {
	tests := []struct {
		name   string
		cursor func(int) string
		pages  int
		err    string
	}{
		{"empty intermediate page", func(page int) string {
			if page == 1 {
				return "next"
			}
			return ""
		}, 2, ""},
		{"repeated cursor", func(page int) string {
			return []string{"first", "second"}[(page-1)%2]
		}, 3, "repeated cursor"},
		{"page cap", func(page int) string { return fmt.Sprint(page) }, maxResultPages, "exceeded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pages atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				page := int(pages.Add(1))
				_, _ = fmt.Fprintf(w, `{"metadata":{"continue":%q},"items":[]}`, tt.cursor(page))
			}))
			defer server.Close()
			client := newEventTestClient(t, server, "test-token")
			items, err := client.listCurrentResults(t.Context())
			if tt.err == "" {
				if err != nil || len(items) != 0 {
					t.Fatalf("expected empty complete result, got %d items and error %v", len(items), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("expected pagination error %q, got %v", tt.err, err)
			}
			if got := int(pages.Load()); got != tt.pages {
				t.Fatalf("queried %d pages, expected %d", got, tt.pages)
			}
		})
	}
}

func TestKubernetesRequestTimesOutWhenUpstreamStalls(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newEventTestClient(t, server, "test-token")

	ctx, cancel := context.WithTimeout(t.Context(), kubernetesRequestTimeout+3*time.Second)
	defer cancel()
	_, err := client.ListNamespaces(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("expected the backend client deadline to expire first, got client=%v caller=%v", err, ctx.Err())
	}
}
