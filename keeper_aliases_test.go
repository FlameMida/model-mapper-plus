package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeeperAliasesCacheAndForcedRefresh(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]string{{"apiKey": "sk-a", "keyAlias": time.Unix(int64(n), 0).Format(time.RFC3339)}}})
	}))
	defer server.Close()
	client, err := newKeeperClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	service := newKeeperAliasService(client, nil)
	now := time.Now()
	service.now = func() time.Time { return now }
	a := service.get(context.Background(), false)
	b := service.get(context.Background(), false)
	if a.Status != "ready" || a.Items[0].Alias != b.Items[0].Alias || calls.Load() != 1 {
		t.Fatalf("a=%+v b=%+v calls=%d", a, b, calls.Load())
	}
	b.Items[0].Alias = "modified by caller"
	if got := service.get(context.Background(), false); got.Items[0].Alias != a.Items[0].Alias {
		t.Fatal("caller mutated cache")
	}
	c := service.get(context.Background(), true)
	if c.Status != "ready" || c.Items[0].Alias == a.Items[0].Alias || calls.Load() != 2 {
		t.Fatal("forced refresh did not fetch")
	}
	now = now.Add(61 * time.Second)
	if d := service.get(context.Background(), false); d.Status != "ready" || calls.Load() != 3 {
		t.Fatal("expired cache did not fetch")
	}
}

func TestKeeperAliasesCoalescesConcurrentRefresh(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		_, _ = w.Write([]byte(`{"items":[{"apiKey":"sk-a","keyAlias":"A"}]}`))
	}))
	defer server.Close()
	client, _ := newKeeperClient(server.URL, "")
	service := newKeeperAliasService(client, nil)
	done := make(chan keeperAliasesResponse, 1)
	go func() { done <- service.get(context.Background(), true) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := service.get(ctx, true)
	if got.Status != "unavailable" || got.ErrorCode != "timeout" {
		t.Fatalf("wait cancellation=%+v", got)
	}
	if calls.Load() != 1 {
		t.Fatal("created duplicate request")
	}
	close(release)
	if got := <-done; got.Status != "ready" {
		t.Fatalf("first request=%+v", got)
	}
}

func TestKeeperAliasesFailureAndCooldown(t *testing.T) {
	var status atomic.Int32
	status.Store(200)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"items":[{"apiKey":"sk-a","keyAlias":"A"}]}`))
	}))
	defer server.Close()
	client, _ := newKeeperClient(server.URL, "")
	service := newKeeperAliasService(client, nil)
	now := time.Now()
	service.now = func() time.Time { return now }
	if got := service.get(context.Background(), false); got.Status != "ready" {
		t.Fatal(got)
	}
	status.Store(429)
	got := service.get(context.Background(), true)
	if got.Status != "unavailable" || got.ErrorCode != "rate_limited" || len(got.Items) != 0 || got.RetryAfterSeconds != 120 {
		t.Fatalf("failure=%+v", got)
	}
	status.Store(200)
	if got := service.get(context.Background(), true); got.Status != "unavailable" || calls.Load() != 2 {
		t.Fatal("forced refresh bypassed cooldown")
	}
	now = now.Add(121 * time.Second)
	if got := service.get(context.Background(), false); got.Status != "ready" || calls.Load() != 3 {
		t.Fatal("did not recover")
	}
	status.Store(403)
	if got := service.get(context.Background(), true); got.ErrorCode != "authentication_failed" || got.RetryAfterSeconds != 60 {
		t.Fatalf("auth failure=%+v", got)
	}
	if got := service.get(context.Background(), false); got.Status != "unavailable" || calls.Load() != 4 {
		t.Fatal("cached success survived failure")
	}
}

func TestKeeperAliasesConfigurationChangeDiscardsInflight(t *testing.T) {
	resetKeeperAliases()
	defer resetKeeperAliases()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		_, _ = w.Write([]byte(`{"items":[{"apiKey":"sk-a","keyAlias":"OLD"}]}`))
	}))
	defer old.Close()
	fresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"apiKey":"sk-a","keyAlias":"NEW"}]}`))
	}))
	defer fresh.Close()
	done := make(chan keeperAliasesResponse, 1)
	oldConfig := Config{UsageKeeperURL: old.URL}
	setupManagementTest(t, oldConfig)
	go func() { done <- keeperAliasesForConfig(oldConfig, true) }()
	<-entered
	current := Config{UsageKeeperURL: fresh.URL}
	setLoadedConfigForTest(current)
	if got := keeperAliasesForConfig(current, false); got.Status != "ready" || got.Items[0].Alias != "NEW" {
		t.Fatal(got)
	}
	close(release)
	if got := <-done; got.Status == "ready" {
		t.Fatal("old configuration returned ready")
	}
	if got := keeperAliasesForConfig(current, false); got.Items[0].Alias != "NEW" {
		t.Fatal("old result polluted current cache")
	}
}

type keeperWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *keeperWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestKeeperAliasesSuccessfulWaitersShareRefresh(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		_, _ = w.Write([]byte(`{"items":[{"apiKey":"sk-a","keyAlias":"A"}]}`))
	}))
	defer server.Close()
	client, _ := newKeeperClient(server.URL, "")
	service := newKeeperAliasService(client, nil)
	results := make(chan keeperAliasesResponse, 4)
	go func() { results <- service.get(context.Background(), true) }()
	<-entered
	for i := 0; i < 3; i++ {
		ctx := &keeperWaitContext{Context: context.Background(), waiting: make(chan struct{})}
		go func() { results <- service.get(ctx, true) }()
		<-ctx.waiting
	}
	close(release)
	for i := 0; i < 4; i++ {
		got := <-results
		if got.Status != "ready" || len(got.Items) != 1 || got.Items[0].Alias != "A" {
			t.Fatal(got)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("got %d upstream calls", calls.Load())
	}
}
