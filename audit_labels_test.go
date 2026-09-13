package main

import (
	"strconv"
	"sync"
	"testing"
)

func resetAuditLabelCacheForTest(t *testing.T) {
	t.Helper()
	auditLabelCache.Lock()
	auditLabelCache.supplier = nil
	auditLabelCache.credential = nil
	auditLabelCache.Unlock()
	t.Cleanup(func() {
		auditLabelCache.Lock()
		auditLabelCache.supplier = nil
		auditLabelCache.credential = nil
		auditLabelCache.Unlock()
	})
}

// Scenario S7: resolved credentials record supplier and credential labels the
// audit writer can snapshot; misses and empty caches return nothing.
func TestAuditLabelCacheRecordAndLookup(t *testing.T) {
	resetAuditLabelCacheForTest(t)
	if auditChannelLabels([]string{"anything"}) != nil {
		t.Fatal("empty cache must return nil")
	}
	recordChannelLabels([]channelCredential{
		{ID: "codex:abc123", Provider: "openai-compatible-volc", ProviderLabel: "火山方舟", Label: "Key ••••1234"},
		{ID: "gemini:def456", Provider: "gemini", Label: "无需 API Key"},
		{ID: "x:y", Provider: "p", Label: "  "},
	})
	labels := auditChannelLabels([]string{"openai-compatible-volc", "codex:abc123", "gemini:def456", "x:y", "unknown", ""})
	want := map[string]string{
		"openai-compatible-volc": "火山方舟",
		"codex:abc123":           "火山方舟 · Key ••••1234",
		"gemini:def456":          "无需 API Key",
	}
	if len(labels) != len(want) {
		t.Fatalf("labels=%v", labels)
	}
	for id, label := range want {
		if labels[id] != label {
			t.Fatalf("%s=%q want %q", id, labels[id], label)
		}
	}
	// First non-empty label wins; later resolutions cannot erase it.
	recordChannelLabels([]channelCredential{{ID: "codex:abc123", Provider: "openai-compatible-volc", ProviderLabel: "改名后", Label: "Key ••••9999"}})
	if labels = auditChannelLabels([]string{"codex:abc123", "openai-compatible-volc"}); labels["codex:abc123"] != "火山方舟 · Key ••••1234" || labels["openai-compatible-volc"] != "火山方舟" {
		t.Fatalf("first label overwritten: %v", labels)
	}
}

// Capacity cap: beyond the limit no new entries are added, without eviction.
func TestAuditLabelCacheCapacity(t *testing.T) {
	resetAuditLabelCacheForTest(t)
	rows := make([]channelCredential, 0, auditLabelCacheLimit)
	for i := 0; i < auditLabelCacheLimit; i++ {
		rows = append(rows, channelCredential{ID: "id-" + strconv.Itoa(i), Label: "L"})
	}
	recordChannelLabels(rows)
	auditLabelCache.RLock()
	size := len(auditLabelCache.supplier) + len(auditLabelCache.credential)
	auditLabelCache.RUnlock()
	if size > auditLabelCacheLimit {
		t.Fatalf("cache grew past limit: %d", size)
	}
	recordChannelLabels([]channelCredential{{ID: "overflow", Label: "L"}, {Provider: "overflow-provider", ProviderLabel: "P"}})
	if auditChannelLabels([]string{"overflow", "overflow-provider"}) != nil {
		t.Fatal("cache accepted entries past the limit")
	}
	if auditChannelLabels([]string{"id-1"})["id-1"] != "L" {
		t.Fatal("capacity check evicted existing entries")
	}
}

func TestAuditLabelCacheConcurrentAccess(t *testing.T) {
	resetAuditLabelCacheForTest(t)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				recordChannelLabels([]channelCredential{{ID: "c", Provider: "p", ProviderLabel: "L", Label: "K"}})
				auditChannelLabels([]string{"c", "p", "other"})
			}
		}(worker)
	}
	wg.Wait()
}
