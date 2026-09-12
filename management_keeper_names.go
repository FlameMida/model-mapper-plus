package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func managementKeeperAuthNames(force bool) pluginapi.ManagementResponse {
	response := managementJSON(http.StatusOK, keeperAuthNamesForConfig(loadedConfig(), force))
	response.Headers.Set("Cache-Control", "no-store")
	return response
}

type keeperAuthNameUpdateResponse struct {
	Status    string          `json:"status"`
	Item      *keeperAuthName `json:"item,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
}

func keeperNameAuditOutcome(status string) string {
	switch status {
	case "ready":
		return "succeeded"
	case "unknown":
		return "unknown"
	default:
		return "failed"
	}
}

func managementPatchKeeperAuthName(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	return withAuditedManagement(req, func() (pluginapi.ManagementResponse, auditResult) {
		result, before := updateKeeperAuthName(req.Body)
		changed := false
		audit := auditResult{Outcome: keeperNameAuditOutcome(result.Status), Changed: &changed, ErrorCode: result.ErrorCode}
		if result.Status == "unknown" {
			audit.Changed = nil
		}
		if result.Status == "ready" {
			changed = *before != *result.Item
			audit.Changes = map[string]auditChange{}
			for field, values := range map[string][2]string{"alias": {before.Alias, result.Item.Alias}, "display_name": {before.DisplayName, result.Item.DisplayName}} {
				if values[0] != values[1] {
					old, _ := json.Marshal(values[0])
					newValue, _ := json.Marshal(values[1])
					audit.Changes[field] = auditChange{Before: old, After: newValue}
				}
			}
		}
		response := managementJSON(http.StatusOK, result)
		response.Headers.Set("Cache-Control", "no-store")
		return response, audit
	})
}

func validKeeperName(alias string) bool {
	if !utf8.ValidString(alias) || utf8.RuneCountInString(alias) > 50 {
		return false
	}
	for _, r := range alias {
		if unicode.IsControl(r) || r == '\u061c' || r == '\u180e' || r == '\u200b' || r == '\u200c' || r == '\u2060' || r == '\ufeff' || r == '\u200e' || r == '\u200f' || r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069' {
			return false
		}
	}
	return true
}

func keeperNameError(status, code string) (keeperAuthNameUpdateResponse, *keeperAuthName) {
	return keeperAuthNameUpdateResponse{Status: status, ErrorCode: code}, nil
}

func keeperNameErrorCode(err error) string {
	var ke *keeperError
	if errors.As(err, &ke) {
		return ke.Code
	}
	return "connection_failed"
}

func updateKeeperAuthName(raw []byte) (keeperAuthNameUpdateResponse, *keeperAuthName) {
	var input struct {
		AuthIndex string  `json:"auth_index"`
		Alias     *string `json:"alias"`
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &input) != nil || strings.TrimSpace(input.AuthIndex) == "" || input.Alias == nil {
		return keeperNameError("invalid", "invalid_request")
	}
	alias := strings.TrimSpace(*input.Alias)
	if !validKeeperName(alias) {
		return keeperNameError("invalid", "invalid_alias")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	service, code := keeperNameServiceForConfig(loadedConfig())
	if code != "" || service == nil || service.configError != nil {
		return keeperNameError("unavailable", "configuration_error")
	}
	items, err := service.client.fetchAuthNames(ctx)
	if err != nil {
		return keeperNameError("unavailable", keeperNameErrorCode(err))
	}
	var before *keeperAuthName
	for i := range items {
		if items[i].AuthIndex == input.AuthIndex {
			before = &items[i]
			break
		}
	}
	if before == nil {
		return keeperNameError("not_found", "not_found")
	}
	if ctx.Err() != nil {
		return keeperNameError("unavailable", "timeout")
	}
	payload, _ := json.Marshal(map[string]string{"alias": alias})
	published := false
	defer func() {
		if !published {
			service.publishUpdate(nil)
		}
	}()
	response, status, err := service.client.patchAuthName(ctx, before.IdentityID, payload)
	if err != nil {
		return keeperNameError(status, keeperNameErrorCode(err))
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotFound:
		return keeperNameError("not_found", "not_found")
	case http.StatusBadRequest:
		return keeperNameError("invalid", "invalid_alias")
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return keeperNameError("unavailable", keeperNameErrorCode(keeperHTTPError(response)))
	case http.StatusOK:
	default:
		return keeperNameError("unknown", keeperNameErrorCode(keeperHTTPError(response)))
	}
	body, err := readKeeperBody(ctx, response.Body)
	if err != nil {
		return keeperNameError("unknown", keeperNameErrorCode(err))
	}
	rows, err := parseKeeperAuthNames(append(append([]byte(`{"identities":[`), body...), []byte(`]}`)...))
	if err != nil || len(rows) != 1 || rows[0].AuthIndex != input.AuthIndex || rows[0].IdentityID != before.IdentityID {
		return keeperNameError("unknown", "invalid_response")
	}
	updated := rows[0]
	old := *before
	*before = updated
	service.publishUpdate(items)
	published = true
	return keeperAuthNameUpdateResponse{Status: "ready", Item: &updated}, &old
}
