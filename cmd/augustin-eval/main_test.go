package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vichanse/augustin/internal/config"
	"github.com/vichanse/augustin/internal/model"
)

func env(vars map[string]string) config.Lookup {
	return func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
}

func forbidden(t *testing.T) func(config.Lookup) (model.Provider, error) {
	return func(config.Lookup) (model.Provider, error) {
		t.Fatal("a real provider was created")
		return nil, nil
	}
}

var consented = map[string]string{"AUGUSTIN_PROVIDER": "openai", "AUGUSTIN_MODEL": "m", "AUGUSTIN_ALLOW_REMOTE_DATA": "true", "OPENAI_API_KEY": "sk-test"}

// Test 17: deterministic mode is the default and never creates a provider,
// even when a key and consent are configured.
func TestDeterministicModeNeverContactsAProvider(t *testing.T) {
	var out, errOut bytes.Buffer
	report := filepath.Join(t.TempDir(), "r.json")
	code := run(context.Background(), []string{"--case", "pagination", "--json", report}, &out, &errOut, env(consented), forbidden(t))
	if code != 0 || !strings.Contains(out.String(), "pagination/reference") || !strings.Contains(out.String(), "RESOLVED") {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errOut.String())
	}
	var doc struct {
		Mode, Provider string
		Attempts       []json.RawMessage
	}
	data, _ := os.ReadFile(report)
	if json.Unmarshal(data, &doc) != nil || doc.Mode != "deterministic" || doc.Provider != "scripted" || len(doc.Attempts) != 2 {
		t.Fatalf("report: %s", data)
	}
}

// Test 18: real mode needs the explicit flag, a selection and remote-data consent.
func TestRealModeRequiresOptInAndConsent(t *testing.T) {
	noConsent := map[string]string{"AUGUSTIN_PROVIDER": "openai", "AUGUSTIN_MODEL": "m", "OPENAI_API_KEY": "sk-test"}
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"no consent":   {[]string{"--mode", "real", "--case", "discount"}, noConsent, "AUGUSTIN_ALLOW_REMOTE_DATA"},
		"no model":     {[]string{"--mode", "real", "--all"}, map[string]string{"AUGUSTIN_PROVIDER": "openai", "AUGUSTIN_ALLOW_REMOTE_DATA": "true"}, "AUGUSTIN_MODEL"},
		"no selection": {[]string{"--mode", "real"}, consented, "Usage"},
		"bad repeat":   {[]string{"--mode", "real", "--all", "--repeat", "0"}, consented, "Usage"},
		"unknown mode": {[]string{"--mode", "live", "--all"}, consented, "Usage"},
	} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), tc.args, &out, &errOut, env(tc.env), forbidden(t)); code != 2 || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%s: exit %d, stderr %q", name, code, errOut.String())
		}
	}
}

func TestRealModeUsesConfiguredProviderPerAttempt(t *testing.T) {
	created := 0
	factory := func(config.Lookup) (model.Provider, error) {
		created++
		return nil, os.ErrPermission // stands in for a provider that cannot start
	}
	var out, errOut bytes.Buffer
	code := run(context.Background(), []string{"--mode", "real", "--case", "discount", "--repeat", "2"}, &out, &errOut, env(consented), factory)
	if code != 1 || created != 2 || !strings.Contains(out.String(), "PROVIDER_ERROR") || !strings.Contains(errOut.String(), "not an OS sandbox") {
		t.Fatalf("exit %d created %d\n%s%s", code, created, out.String(), errOut.String())
	}
}

// Test 20 (CLI): a canceled evaluation exits 130 with partial results.
func TestCanceledEvaluationExits130(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	if code := run(ctx, []string{"--case", "discount"}, &out, &errOut, env(nil), forbidden(t)); code != 130 || !strings.Contains(out.String(), "Canceled") {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errOut.String())
	}
}

func TestListShowsCasesAndScenarios(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"--list"}, &out, &errOut, env(nil), forbidden(t)); code != 0 || !strings.Contains(out.String(), "regression-trap") || !strings.Contains(out.String(), "superficial") {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}
