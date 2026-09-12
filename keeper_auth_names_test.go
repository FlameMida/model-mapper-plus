package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestKeeperNamesCacheRefreshExpiryAndFailures(t *testing.T) {
	var calls, status atomic.Int32
	status.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(int(status.Load()))
		_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("17", "idx-a", "A", "A")}})
	}))
	defer server.Close()
	client, err := newKeeperClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	service := newKeeperAuthNameService(client, nil)
	now := time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return now }
	a := service.get(context.Background(), false)
	if a.Status != "ready" || len(a.Items) != 1 || a.FetchedAt != "2026-09-12T01:02:03Z" {
		t.Fatal(a)
	}
	a.Items[0].Alias = "caller edit"
	b := service.get(context.Background(), false)
	if b.Status != "ready" || b.Items[0].Alias != "A" || calls.Load() != 1 {
		t.Fatalf("cache=%+v calls=%d", b, calls.Load())
	}
	if got := service.get(context.Background(), true); got.Status != "ready" || calls.Load() != 2 {
		t.Fatalf("refresh=%+v calls=%d", got, calls.Load())
	}
	now = now.Add(60 * time.Second)
	if got := service.get(context.Background(), false); got.Status != "ready" || calls.Load() != 3 {
		t.Fatalf("expiry=%+v calls=%d", got, calls.Load())
	}
	status.Store(429)
	if got := service.get(context.Background(), true); got.Status != "unavailable" || got.ErrorCode != "rate_limited" || got.RetryAfterSeconds != 120 || len(got.Items) != 0 {
		t.Fatal(got)
	}
	now = now.Add(500 * time.Millisecond)
	if got := service.get(context.Background(), true); got.RetryAfterSeconds != 120 || calls.Load() != 4 {
		t.Fatalf("cooldown=%+v calls=%d", got, calls.Load())
	}
	status.Store(200)
	now = now.Add(120 * time.Second)
	if got := service.get(context.Background(), true); got.Status != "ready" || calls.Load() != 5 {
		t.Fatal(got)
	}
	status.Store(403)
	if got := service.get(context.Background(), true); got.ErrorCode != "authentication_failed" || got.RetryAfterSeconds != 60 {
		t.Fatal(got)
	}
	if got := service.get(context.Background(), false); got.Status != "unavailable" || calls.Load() != 6 {
		t.Fatal(got)
	}
	now = now.Add(60 * time.Second)
	status.Store(200)
	if got := service.get(context.Background(), false); got.Status != "ready" || calls.Load() != 7 {
		t.Fatal(got)
	}
}

func TestKeeperNamesCoalescesAndWaiterCanCancel(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("17", "idx-a", "A", "A")}})
	}))
	defer server.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	client, err := newKeeperClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	service := newKeeperAuthNameService(client, nil)
	first := make(chan keeperAuthNamesResponse, 1)
	go func() { first <- service.get(context.Background(), true) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := service.get(ctx, true); got.ErrorCode != "timeout" || calls.Load() != 1 {
		t.Fatalf("cancel=%+v calls=%d", got, calls.Load())
	}
	// A bounded waiter also makes a missing-coalescing implementation terminate.
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	waiting := &keeperWaitContext{Context: waitCtx, waiting: make(chan struct{})}
	second := make(chan keeperAuthNamesResponse, 1)
	go func() { second <- service.get(waiting, true) }()
	<-waiting.waiting
	// Give an accidental second HTTP request time to reach the real server.
	<-time.After(30 * time.Millisecond)
	releaseOnce.Do(func() { close(release) })
	for _, result := range []keeperAuthNamesResponse{<-first, <-second} {
		if result.Status != "ready" || len(result.Items) != 1 || result.Items[0].Alias != "A" {
			t.Fatal(result)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate upstream reads=%d", calls.Load())
	}
}

func TestKeeperNamesS4ConfigurationRejectsStaleAndInflight(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("17", "idx-a", "OLD", "OLD")}})
	}))
	defer old.Close()
	fresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("18", "idx-b", "NEW", "NEW")}})
	}))
	defer fresh.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	setupManagementTest(t, Config{UsageKeeperURL: old.URL})
	snapshot := loadedConfig()
	done := make(chan keeperAuthNamesResponse, 1)
	go func() { done <- keeperAuthNamesForConfig(snapshot, true) }()
	<-entered
	current := Config{UsageKeeperURL: fresh.URL}
	setLoadedConfigForTest(current)
	if got := keeperAuthNamesForConfig(current, false); got.Status != "ready" || got.Items[0].Alias != "NEW" {
		t.Fatal(got)
	}
	releaseOnce.Do(func() { close(release) })
	if got := <-done; got.Status != "unavailable" || got.ErrorCode != "configuration_error" {
		t.Fatalf("late response=%+v", got)
	}
	if got := keeperAuthNamesForConfig(snapshot, true); got.Status != "unavailable" || got.ErrorCode != "configuration_error" {
		t.Fatalf("stale config=%+v", got)
	}
	if got := keeperAuthNamesForConfig(current, false); got.Status != "ready" || got.Items[0].Alias != "NEW" {
		t.Fatal(got)
	}
}

func TestKeeperNamesResetAndIndependentAliases(t *testing.T) {
	var names, aliases atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/usage/identities" {
			names.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"identities": []any{keeperIdentityFixture("17", "idx-a", "A", "A")}})
			return
		}
		aliases.Add(1)
		_, _ = w.Write([]byte(`{"items":[{"apiKey":"sk-a","keyAlias":"key alias"}]}`))
	}))
	defer server.Close()
	setupManagementTest(t, Config{UsageKeeperURL: server.URL})
	for range 2 {
		var body keeperAuthNamesResponse
		decodeBody(t, managementKeeperAuthNames(false), &body)
		if body.Status != "ready" {
			t.Fatal(body)
		}
		var keys keeperAliasesResponse
		decodeBody(t, managementKeeperAliases(false), &keys)
		if keys.Status != "ready" || keys.Items[0].Alias != "key alias" {
			t.Fatal(keys)
		}
	}
	if names.Load() != 1 || aliases.Load() != 1 {
		t.Fatalf("names=%d aliases=%d", names.Load(), aliases.Load())
	}
	resetKeeperAliases()
	var body keeperAuthNamesResponse
	decodeBody(t, managementKeeperAuthNames(false), &body)
	if body.Status != "ready" || names.Load() != 2 {
		t.Fatal("reset retained names cache")
	}
}

func TestKeeperNamesInFlightDoesNotBlockStateReadOrSave(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"identities":[]}`))
	}))
	defer server.Close()
	defer close(release)
	setupManagementTest(t, Config{Enabled: true, UsageKeeperURL: server.URL})
	go managementKeeperAuthNames(true)
	<-entered
	done := make(chan bool, 1)
	go func() {
		read := managementGetState()
		save := managementPostKey(pluginapi.ManagementRequest{Body: []byte(`{"key":"sk-names-independent","alias":"Local","enabled":true}`)})
		done <- read.StatusCode == 200 && save.StatusCode < 300
	}()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("state operation failed")
		}
	case <-time.After(time.Second):
		t.Fatal("Keeper names IO blocked state operations")
	}
}

func TestKeeperNamesS4OneDeadlineIncludesLogin(t *testing.T) {
	t.Setenv("KEEPER_NAMES_DEADLINE_PASSWORD", "secret")
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/login" {
			logins.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			<-r.Context().Done()
			return
		}
		w.WriteHeader(401)
	}))
	defer server.Close()
	client, err := newKeeperClient(server.URL, "KEEPER_NAMES_DEADLINE_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	service := newKeeperAuthNameService(client, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if got := service.get(ctx, true); got.ErrorCode != "timeout" || logins.Load() != 1 {
		t.Fatal(got)
	}
}
