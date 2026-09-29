package copyjob

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
)

func TestPlatformClientFailClosedWhenUnconfigured(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	client := NewPlatformClient(config.Config{TaskBaseURL: srv.URL, TaskToken: "t", AppID: "touch-engine"})
	if client.Ready() {
		t.Fatal("partial config was ready")
	}
	_, err := client.Generate(context.Background(), GenerateRequest{IdempotencyKey: "k", Prompt: "门店名:江边小馆"})
	if err != ErrNotConfigured {
		t.Fatalf("err = %v", err)
	}
	if hits != 0 {
		t.Fatalf("unconfigured client called the network %d times", hits)
	}
}

func TestPlatformClientQuotesBeforeTaskAndOmitsProject(t *testing.T) {
	var taskPosts int
	var taskRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/usage"):
			_, _ = w.Write([]byte(`{"ok":true,"fact":{"usage_id":"use_1"}}`))
		case strings.HasSuffix(r.URL.Path, "/quote"):
			_, _ = w.Write([]byte(`{"ok":true,"quote":{"amount_minor":12}}`))
		case r.URL.Path == "/internal/v1/tasks":
			taskPosts++
			taskRaw = string(body)
			if bytesContainsProject(body) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"task_id":"tsk_1","status":"SUCCEEDED","hold_id":"hold_1","result_json":"{\"title\":\"周末到店\",\"intro\":\"到店有礼\",\"topics\":[\"#到店\"]}"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := readyClient(srv.URL)
	got, err := client.Generate(context.Background(), GenerateRequest{IdempotencyKey: "job-1", PayerAccountID: "ten_a", SnapshotHash: "abc", Prompt: "门店名:江边小馆"})
	if err != nil {
		t.Fatal(err)
	}
	if taskPosts != 1 || got.TaskID != "tsk_1" || !got.Submitted || got.AmountMinor != 12 || got.Output.Title != "周末到店" {
		t.Fatalf("result posts %d %+v", taskPosts, got)
	}
	if bytesContainsProject([]byte(taskRaw)) || !strings.Contains(taskRaw, `"capability":"touch.ai_copy"`) {
		t.Fatalf("task body = %s", taskRaw)
	}
}

func TestPlatformClientDoesNotSubmitTaskWhenQuoteFails(t *testing.T) {
	var taskPosts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/usage") {
			_, _ = w.Write([]byte(`{"ok":true,"fact":{"usage_id":"use_1"}}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/quote") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"ok":false}`))
			return
		}
		taskPosts++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	client := readyClient(srv.URL)
	_, err := client.Generate(context.Background(), GenerateRequest{IdempotencyKey: "job-1", PayerAccountID: "ten_a", Prompt: "门店名:江边小馆"})
	if err == nil || taskPosts != 0 {
		t.Fatalf("err %v posts %d", err, taskPosts)
	}
}

func readyClient(base string) *PlatformClient {
	return &PlatformClient{
		Enabled:     true,
		TaskBaseURL: base, TaskToken: "task", BillingBaseURL: base, BillingToken: "bill",
		AppID: "touch-engine", Provider: "copy", ModelID: "copy-small", ModelVersion: "2026-09-29",
		PricingVersion: "2026-09", HTTP: &http.Client{},
	}
}

func bytesContainsProject(body []byte) bool {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return true
	}
	if _, ok := raw["project_id"]; ok {
		return true
	}
	blob := string(body)
	return strings.Contains(blob, "goboost") || strings.Contains(blob, "project_id")
}
