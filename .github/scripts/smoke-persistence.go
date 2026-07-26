package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// smoke-persistence verifies that rules saved to state_file survive a CPA
// restart (ADR-0001: state_file is the source of truth once it exists).
//
// Flow: inject a rule (fake-name => real-model) -> confirm it routes -> restart
// CPA (CPA_SMOKE_RESTART_CMD) -> confirm the SAME rule still routes without
// re-injecting -> cleanup. A failure after restart means state was lost.
//
// Required env (shares .env with smoke-local):
//
//	CPA_SMOKE_MGMT_KEY, CPA_SMOKE_CLIENT_KEY
//	CPA_SMOKE_RESTART_CMD   e.g. "docker compose -f .../docker-compose.yml restart cli-proxy-api"
//
// Optional:
//
//	CPA_SMOKE_BASE_URL=http://127.0.0.1:8317
//	CPA_SMOKE_MODEL_KEY_TEST=keytest-src
//	CPA_SMOKE_MODEL_PASSTHROUGH=deepseek-v4-flash

const (
	defaultBaseURL    = "http://127.0.0.1:8317"
	defaultKeyTest    = "keytest-src"
	defaultPassthrough = "deepseek-v4-flash"
	rulesEndpoint     = "/v0/management/plugins/model-mapper-plus/rules"
)

type env struct {
	baseURL    string
	mgmtKey    string
	clientKey  string
	restartCmd string
	keyTest    string
	passthrough string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func envVal(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func mustEnv(key string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%s is required", key)
}

func run() (retErr error) {
	mgmtKey, err := mustEnv("CPA_SMOKE_MGMT_KEY")
	if err != nil {
		return err
	}
	clientKey, err := mustEnv("CPA_SMOKE_CLIENT_KEY")
	if err != nil {
		return err
	}
	restartCmd, err := mustEnv("CPA_SMOKE_RESTART_CMD")
	if err != nil {
		return errors.New("CPA_SMOKE_RESTART_CMD is required (e.g. \"docker compose -f .../docker-compose.yml restart cli-proxy-api\")")
	}
	e := env{
		baseURL:     envVal("CPA_SMOKE_BASE_URL", defaultBaseURL),
		mgmtKey:     mgmtKey,
		clientKey:   clientKey,
		restartCmd:  restartCmd,
		keyTest:     envVal("CPA_SMOKE_MODEL_KEY_TEST", defaultKeyTest),
		passthrough: envVal("CPA_SMOKE_MODEL_PASSTHROUGH", defaultPassthrough),
	}

	defer func() {
		// best-effort cleanup; retry because CPA may still be restarting.
		for i := 0; i < 3; i++ {
			if err := clearRules(e); err == nil {
				return
			}
			fmt.Fprintln(os.Stderr, ">> cleanup clearRules failed, retrying...")
			time.Sleep(2 * time.Second)
		}
	}()

	fmt.Println(">> waiting for CPA ready (before)...")
	if err := waitReady(e); err != nil {
		return err
	}
	fmt.Printf(">> injecting rule %s=>%s\n", e.keyTest, e.passthrough)
	if err := putRule(e, e.keyTest+"=>"+e.passthrough); err != nil {
		return fmt.Errorf("inject rule: %w", err)
	}

	fmt.Println(">> checking rule routes (before restart)...")
	if err := assertRoutes(e, "before"); err != nil {
		return err
	}

	fmt.Printf(">> restarting CPA: %s\n", e.restartCmd)
	if err := exec.Command("sh", "-c", e.restartCmd).Run(); err != nil {
		return fmt.Errorf("restart CPA: %w", err)
	}

	fmt.Println(">> waiting for CPA ready (after restart)...")
	if err := waitReady(e); err != nil {
		return err
	}
	fmt.Println(">> checking rule STILL routes (after restart, no re-inject)...")
	if err := assertRoutes(e, "after"); err != nil {
		return err
	}
	fmt.Println("ok: state survives CPA restart")
	return nil
}

// assertRoutes confirms the fake keyTest name reaches upstream successfully,
// i.e. the saved rule keyTest=>passthrough is still in effect.
func assertRoutes(e env, phase string) error {
	st, body, err := sendChat(e, e.keyTest)
	if err != nil {
		return fmt.Errorf("%s: request: %w", phase, err)
	}
	if st/100 != 2 {
		return fmt.Errorf("%s: want rule to route (success), got status=%d body=%s", phase, st, body)
	}
	return nil
}

func waitReady(e env) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		st, _, err := getModels(e)
		if err == nil && st/100 == 2 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("CPA readiness timeout")
}

func getModels(e env) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+e.clientKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

func putRule(e env, openaiRules string) error {
	body := map[string]string{"global": "", "claude": "", "codex": "", "openai": openaiRules}
	raw, _ := json.Marshal(body)
	st, respBody, err := doReq(e, http.MethodPut, rulesEndpoint, raw)
	if err != nil {
		return err
	}
	if st/100 != 2 {
		return fmt.Errorf("status=%d body=%s", st, respBody)
	}
	return nil
}

func clearRules(e env) error {
	return putRule(e, "")
}

func sendChat(e env, model string) (int, []byte, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "say ok"}},
	})
	return doReq(e, http.MethodPost, "/v1/chat/completions", payload)
}

func doReq(e env, method, path string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, e.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	// management calls use the mgmt key; chat calls use the client key.
	if strings.Contains(path, "/management/") {
		req.Header.Set("Authorization", "Bearer "+e.mgmtKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+e.clientKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, nil
}
