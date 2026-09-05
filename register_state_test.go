package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pluginabi "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// A fresh library receives plugin.register with config_yaml. The host does not
// send plugin.reconfigure until a later configuration reload.
func TestPluginRegisterLoadsPersistedState(t *testing.T) {
	for _, kind := range []string{"absolute", "relative", "default"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			setLoadedConfigForTest(defaultConfig())
			t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })

			path := filepath.Join(dir, "plugins", defaultStateFile)
			rawYAML := "enabled: true\nglobal_rules: a=>yaml\n"
			switch kind {
			case "absolute":
				rawYAML += "state_file: " + path + "\n"
			case "relative":
				rawYAML += "state_file: plugins/model-mapper-plus-state.json\n"
			case "default":
				path = filepath.Join(dir, defaultStateFile)
			}
			// Legacy version 1 state remains authoritative, including key rules.
			rawState := []byte(`{"version":1,"rules":{"global":"a=>b"},"key_bindings":[{"key":"sk-register","alias":"saved","enabled":true,"rules":{"global":"b=>c"}}]}`)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, rawState, 0o600); err != nil {
				t.Fatal(err)
			}

			registerRaw, err := handleMethod(pluginabi.MethodPluginRegister, lifecycleRaw(t, rawYAML))
			if err != nil {
				t.Fatal(err)
			}
			var env pluginabi.Envelope
			if err := json.Unmarshal(registerRaw, &env); err != nil || !env.OK {
				t.Fatalf("register failed: err=%v envelope=%s", err, registerRaw)
			}
			var reg registration
			if err := json.Unmarshal(env.Result, &reg); err != nil || reg.Metadata.Name != "model-mapper-plus" {
				t.Fatalf("invalid registration: err=%v result=%s", err, env.Result)
			}

			var response stateResponse
			decodeBody(t, managementGetState(), &response)
			if response.StateFile != path || !response.Persisted || response.LoadError != "" {
				t.Fatalf("state after register: path=%q persisted=%v load_error=%q; want %q, true, empty",
					response.StateFile, response.Persisted, response.LoadError, path)
			}
			var want State
			if err := json.Unmarshal(rawState, &want); err != nil {
				t.Fatal(err)
			}
			if response.Version != want.Version || response.Rules != want.Rules || !reflect.DeepEqual(response.KeyBindings, want.KeyBindings) {
				t.Fatalf("saved state not restored: got=%+v want=%+v", response, want)
			}
			decision, err := routeModel(loadedConfig(), loadedRuleSource(), "openai", "a", "sk-register")
			if err != nil || !decision.Handled || decision.UpstreamModel != "c" {
				t.Fatalf("route immediately after register: decision=%+v err=%v", decision, err)
			}
			if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, rawState) {
				t.Fatalf("register modified saved state: err=%v", err)
			}
		})
	}
}

func TestPluginRegisterSeedsMissingStateFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	setLoadedConfigForTest(defaultConfig())
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	raw := lifecycleRaw(t, "enabled: false\nstate_file: "+path+"\nglobal_rules: a=>b\n")
	if _, err := handlePluginRegister(raw); err != nil {
		t.Fatal(err)
	}
	if loadedConfig().Enabled || stateFilePath() != path {
		t.Fatalf("register ignored YAML config: %+v", loadedConfig())
	}
	st, persisted := loadedStateSnapshot()
	if persisted || st.Rules.Global != "a=>b" || loadedStateLoadError() != "" {
		t.Fatalf("register did not load YAML seed: state=%+v persisted=%v error=%q", st, persisted, loadedStateLoadError())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("register must not create missing state: %v", err)
	}
}

func TestPluginRegisterRejectsInvalidConfig(t *testing.T) {
	setLoadedConfigForTest(defaultConfig())
	t.Cleanup(func() { setLoadedConfigForTest(defaultConfig()) })
	raw := lifecycleRaw(t, "enabled: true\nglobal_rules: a => b\n")
	response, err := handleMethod(pluginabi.MethodPluginRegister, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(response, &env); err != nil {
		t.Fatal(err)
	}
	if env.OK || env.Error == nil {
		t.Fatalf("register accepted invalid config: %s", response)
	}
	if got := loadedConfig(); got != defaultConfig() {
		t.Fatalf("invalid config changed runtime: %+v", got)
	}
}
