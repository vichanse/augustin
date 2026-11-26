package config

import (
	"strings"
	"testing"
	"time"
)

func validEnv() map[string]string {
	return map[string]string{
		"AUGUSTIN_PROVIDER":          "openai",
		"AUGUSTIN_MODEL":             "test-model",
		"AUGUSTIN_ALLOW_REMOTE_DATA": "true",
		"OPENAI_API_KEY":             "test-secret",
	}
}

func lookupEnv(env map[string]string) Lookup {
	return func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	}
}

func TestDefaults(t *testing.T) {
	c, err := Load(lookupEnv(validEnv()))
	if err != nil || c.MaxIterations != 10 || c.Budget.MaxToolCalls != 32 || c.Budget.MaxOutputBytes != 2<<20 || c.Budget.Timeout != 120*time.Second {
		t.Fatalf("config=%+v error=%v", c, err)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, name := range []string{"AUGUSTIN_PROVIDER", "AUGUSTIN_MODEL", "AUGUSTIN_ALLOW_REMOTE_DATA", "OPENAI_API_KEY", "AUGUSTIN_MAX_ITERATIONS", "AUGUSTIN_MAX_TOOL_CALLS", "AUGUSTIN_MAX_OUTPUT_BYTES", "AUGUSTIN_TIMEOUT_SECONDS"} {
		t.Run(name, func(t *testing.T) {
			env := validEnv()
			env[name] = ""
			_, err := Load(lookupEnv(env))
			if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOverrides(t *testing.T) {
	env := validEnv()
	env["AUGUSTIN_MAX_ITERATIONS"] = "3"
	env["AUGUSTIN_MAX_TOOL_CALLS"] = "4"
	env["AUGUSTIN_MAX_OUTPUT_BYTES"] = "1024"
	env["AUGUSTIN_TIMEOUT_SECONDS"] = "5"
	c, err := Load(lookupEnv(env))
	if err != nil || c.MaxIterations != 3 || c.Budget.MaxToolCalls != 4 || c.Budget.MaxOutputBytes != 1024 || c.Budget.Timeout != 5*time.Second {
		t.Fatalf("config=%+v error=%v", c, err)
	}
	env["AUGUSTIN_MAX_ITERATIONS"] = "33"
	if _, err := Load(lookupEnv(env)); err == nil {
		t.Fatal("expected upper bound rejection")
	}
}
