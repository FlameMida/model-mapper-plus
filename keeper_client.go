package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type keeperAlias struct {
	Key   string `json:"key"`
	Alias string `json:"alias"`
}

// Errors are deliberately closed codes: URLs, response bodies and credentials
// must never escape through a management response or a logged error.
type keeperError struct {
	Code       string
	RetryAfter time.Duration
}

func (e *keeperError) Error() string { return e.Code }

type keeperClient struct {
	baseURL     string
	passwordEnv string
	http        *http.Client
}

func newKeeperClient(baseURL, passwordEnv string) (*keeperClient, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, &keeperError{Code: "configuration_error"}
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, &keeperError{Code: "configuration_error"}
	}
	return &keeperClient{baseURL: strings.TrimRight(u.String(), "/"), passwordEnv: strings.TrimSpace(passwordEnv), http: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *keeperClient) request(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, &keeperError{Code: "configuration_error"}
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost || method == http.MethodPatch {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, &keeperError{Code: "timeout"}
		}
		return nil, &keeperError{Code: "connection_failed"}
	}
	return resp, nil
}

func keeperHTTPError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &keeperError{Code: "authentication_failed"}
	case http.StatusTooManyRequests:
		retry := 60 * time.Second
		raw := resp.Header.Get("Retry-After")
		if seconds, err := strconv.ParseInt(raw, 10, 32); err == nil && seconds >= 0 {
			retry = time.Duration(seconds) * time.Second
		} else if at, err := http.ParseTime(raw); err == nil {
			retry = time.Until(at)
			if retry < 0 {
				retry = 0
			}
		}
		return &keeperError{Code: "rate_limited", RetryAfter: retry}
	default:
		if resp.StatusCode >= 500 {
			return &keeperError{Code: "connection_failed"}
		}
		return &keeperError{Code: "invalid_response"}
	}
}

// authenticatedRequest retries only an explicit 401, once after login. Callers
// own the response body and share their overall deadline with login and retry.
func (c *keeperClient) authenticatedRequest(ctx context.Context, method, path string, payload []byte) (*http.Response, error) {
	resp, err := c.request(ctx, method, path, payload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		password := os.Getenv(c.passwordEnv)
		if password == "" {
			return nil, &keeperError{Code: "configuration_error"}
		}
		body, _ := json.Marshal(struct {
			Password string `json:"password"`
		}{password})
		login, err := c.request(ctx, http.MethodPost, "/api/v1/auth/login", body)
		if err != nil {
			return nil, err
		}
		_ = login.Body.Close()
		if login.StatusCode != http.StatusOK && login.StatusCode != http.StatusNoContent {
			return nil, keeperHTTPError(login)
		}
		resp, err = c.request(ctx, method, path, payload)
		if err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func (c *keeperClient) fetch(ctx context.Context) ([]keeperAlias, error) {
	resp, err := c.authenticatedRequest(ctx, http.MethodGet, "/api/v1/usage/api-keys/settings", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, keeperHTTPError(resp)
	}
	raw, err := readKeeperBody(ctx, resp.Body)
	if err != nil {
		return nil, err
	}
	var body struct {
		Items json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Items) == 0 || string(body.Items) == "null" {
		return nil, &keeperError{Code: "invalid_response"}
	}
	var rows []struct {
		Key   *string `json:"apiKey"`
		Alias *string `json:"keyAlias"`
	}
	if err := json.Unmarshal(body.Items, &rows); err != nil {
		return nil, &keeperError{Code: "invalid_response"}
	}
	aliases := make([]keeperAlias, 0, len(rows))
	seen := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.Key == nil || strings.TrimSpace(*row.Key) == "" || row.Alias == nil {
			return nil, &keeperError{Code: "invalid_response"}
		}
		alias := strings.TrimSpace(*row.Alias)
		if previous, ok := seen[*row.Key]; ok {
			if previous != alias {
				return nil, &keeperError{Code: "invalid_response"}
			}
			continue
		}
		seen[*row.Key] = alias
		aliases = append(aliases, keeperAlias{Key: *row.Key, Alias: alias})
	}
	return aliases, nil
}

func readKeeperBody(ctx context.Context, body io.Reader) ([]byte, error) {
	const maxBody = 8 << 20
	raw, err := io.ReadAll(io.LimitReader(body, maxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, &keeperError{Code: "timeout"}
		}
		return nil, &keeperError{Code: "connection_failed"}
	}
	if len(raw) > maxBody {
		return nil, &keeperError{Code: "invalid_response"}
	}
	return raw, nil
}
