package httpapi

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestProtectedRouteCoverageMatchesServeMuxRegistrations(t *testing.T) {
	serverSource, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}

	allAPI := map[string]struct{}{}
	registrationPattern := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/v1[^\"]*)"`)
	for _, match := range registrationPattern.FindAllStringSubmatch(string(serverSource), -1) {
		allAPI[match[1]+" "+match[2]] = struct{}{}
	}

	wrapped := map[string]struct{}{}
	wrappedPattern := regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+) (/api/v1[^\"]*)", server\.protectRoute\("([A-Z]+) (/api/v1[^\"]*)"`)
	for _, match := range wrappedPattern.FindAllStringSubmatch(string(serverSource), -1) {
		outer := match[1] + " " + match[2]
		inner := match[3] + " " + match[4]
		if outer != inner {
			t.Fatalf("ServeMux route %q protects different metadata route %q", outer, inner)
		}
		wrapped[outer] = struct{}{}
	}

	metadata := map[string]struct{}{}
	for _, route := range protectedRoutes() {
		if strings.TrimSpace(route.pattern) == "" {
			t.Fatal("protected route contains empty pattern")
		}
		if err := route.capability.Validate(); err != nil {
			t.Fatalf("route %q has invalid capability: %v", route.pattern, err)
		}
		if !route.deferredScope && route.resolveScope == nil {
			t.Fatalf("route %q lacks scope resolver", route.pattern)
		}
		if route.deferredScope && route.resolveScope != nil {
			t.Fatalf("route %q mixes deferred and immediate scope", route.pattern)
		}
		metadata[route.pattern] = struct{}{}
	}

	assertSameRouteSet(t, "ServeMux API routes vs protectRoute wrappers", allAPI, wrapped)
	assertSameRouteSet(t, "ServeMux API routes vs protectedRoute metadata", allAPI, metadata)
	for route := range allAPI {
		parts := strings.SplitN(route, " ", 2)
		if len(parts) != 2 || !requiresAuthentication(parts[1]) {
			t.Fatalf("protected API route does not require authentication: %q", route)
		}
	}
}

func assertSameRouteSet(t *testing.T, name string, want, got map[string]struct{}) {
	t.Helper()
	if len(want) == len(got) {
		equal := true
		for route := range want {
			if _, ok := got[route]; !ok {
				equal = false
				break
			}
		}
		if equal {
			return
		}
	}
	missing := make([]string, 0)
	extra := make([]string, 0)
	for route := range want {
		if _, ok := got[route]; !ok {
			missing = append(missing, route)
		}
	}
	for route := range got {
		if _, ok := want[route]; !ok {
			extra = append(extra, route)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	t.Fatalf("%s drift: missing=%v extra=%v", name, missing, extra)
}
