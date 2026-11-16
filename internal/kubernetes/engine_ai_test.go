package kubernetes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestEngineAIReadsLiveReferencedSecret(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/core.k8sgpt.ai/v1alpha1/namespaces/engine-ns/k8sgpts/engine":
			_, _ = w.Write([]byte(`{"spec":{"ai":{"enabled":true,"backend":"openai","model":"glm","baseUrl":"https://model.example/v4/","secret":{"name":"engine-key","key":"selected"}}}}`))
		case "/api/v1/namespaces/engine-ns/secrets/engine-key":
			keys := []string{"old-fixture", "new-fixture"}
			index := int(reads.Add(1)) - 1
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string][]byte{"selected": []byte(keys[index]), "other": []byte("unused")}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := newEventTestClient(t, server, "service-token")
	client.config.K8sGPTNS, client.config.K8sGPTName = "engine-ns", "engine"
	for _, expected := range []string{"old-fixture", "new-fixture"} {
		got, err := client.EngineAI(t.Context())
		if err != nil || got.Key != expected || got.Model != "glm" || got.Endpoint != "https://model.example/v4/chat/completions" {
			t.Fatalf("unexpected Engine AI configuration, error: %v", err)
		}
	}
}
