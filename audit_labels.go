package main

import (
	"strings"
	"sync"
)

// Channel display names live only in the CPA provider configuration the admin
// UI forwards through POST /channel-credentials; the plugin cannot read that
// configuration itself. Resolving credentials there records a process-local
// label snapshot the audit writer can copy into events without any network
// IO on the audit path. Restart loses the cache, but past events already
// carry their snapshot and the reader falls back to raw IDs.
const auditLabelCacheLimit = 4096

var auditLabelCache struct {
	sync.RWMutex
	supplier   map[string]string
	credential map[string]string
}

func channelCredentialLabel(row channelCredential) string {
	name := strings.TrimSpace(row.ProviderLabel)
	label := strings.TrimSpace(row.Label)
	switch {
	case label == "":
		return name
	case name == "":
		return label
	default:
		return name + " · " + label
	}
}

// recordChannelLabels merges resolved credential rows into the cache; the
// first non-empty label per ID wins so a stale broad resolution cannot erase
// a sharper one, and the total capacity is capped without eviction.
func recordChannelLabels(rows []channelCredential) {
	auditLabelCache.Lock()
	defer auditLabelCache.Unlock()
	if auditLabelCache.supplier == nil {
		auditLabelCache.supplier = map[string]string{}
		auditLabelCache.credential = map[string]string{}
	}
	for _, row := range rows {
		if len(auditLabelCache.supplier)+len(auditLabelCache.credential) >= auditLabelCacheLimit {
			return
		}
		if label := strings.TrimSpace(row.ProviderLabel); label != "" && row.Provider != "" {
			if _, exists := auditLabelCache.supplier[row.Provider]; !exists {
				auditLabelCache.supplier[row.Provider] = label
			}
		}
		if label := channelCredentialLabel(row); label != "" && row.ID != "" {
			if _, exists := auditLabelCache.credential[row.ID]; !exists {
				auditLabelCache.credential[row.ID] = label
			}
		}
	}
}

// auditChannelLabels returns the cached display names for the given channel
// IDs (supplier provider keys first, credential IDs second); misses are
// simply absent so callers keep the raw ID.
func auditChannelLabels(ids []string) map[string]string {
	auditLabelCache.RLock()
	defer auditLabelCache.RUnlock()
	if len(auditLabelCache.supplier) == 0 && len(auditLabelCache.credential) == 0 {
		return nil
	}
	result := map[string]string{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if label, ok := auditLabelCache.supplier[id]; ok && label != "" {
			result[id] = label
			continue
		}
		if label, ok := auditLabelCache.credential[id]; ok && label != "" {
			result[id] = label
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
