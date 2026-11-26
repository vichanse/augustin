// Package config validates runtime settings without network access.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/model/openai"
)

type Config struct {
	Provider      string
	Model         string
	MaxIterations int
	Budget        agent.Budget
}

type Lookup func(string) (string, bool)

// Load requires explicit consent before configuring a remote provider.
// Credentials are deliberately not retained in Config.
func Load(lookup Lookup) (Config, error) {
	var c Config
	if lookup == nil {
		return c, errors.New("environment lookup required")
	}
	get := func(name string) string {
		v, _ := lookup(name)
		return strings.TrimSpace(v)
	}
	c.Provider = get("AUGUSTIN_PROVIDER")
	c.Model = get("AUGUSTIN_MODEL")
	if c.Provider != "openai" {
		return Config{}, errors.New("AUGUSTIN_PROVIDER must be openai")
	}
	if c.Model == "" {
		return Config{}, errors.New("AUGUSTIN_MODEL required")
	}
	if get("AUGUSTIN_ALLOW_REMOTE_DATA") != "true" {
		return Config{}, errors.New("explicit AUGUSTIN_ALLOW_REMOTE_DATA=true required")
	}
	if get("OPENAI_API_KEY") == "" {
		return Config{}, errors.New("OPENAI_API_KEY required")
	}
	number := func(name string, fallback, maximum int) (int, error) {
		raw, exists := lookup(name)
		if !exists {
			return fallback, nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 1 || n > maximum {
			return 0, fmt.Errorf("%s must be an integer between 1 and %d", name, maximum)
		}
		return n, nil
	}
	var err error
	c.MaxIterations, err = number("AUGUSTIN_MAX_ITERATIONS", 10, 32)
	if err != nil {
		return Config{}, err
	}
	c.Budget.MaxToolCalls, err = number("AUGUSTIN_MAX_TOOL_CALLS", 32, 128)
	if err != nil {
		return Config{}, err
	}
	c.Budget.MaxOutputBytes, err = number("AUGUSTIN_MAX_OUTPUT_BYTES", 2<<20, 2<<20)
	if err != nil {
		return Config{}, err
	}
	seconds, err := number("AUGUSTIN_TIMEOUT_SECONDS", 120, 600)
	if err != nil {
		return Config{}, err
	}
	c.Budget.Timeout = time.Duration(seconds) * time.Second
	return c, nil
}

// NewProvider validates settings again and retrieves credentials only when needed.
func NewProvider(lookup Lookup) (model.Provider, error) {
	c, err := Load(lookup)
	if err != nil {
		return nil, err
	}
	key, _ := lookup("OPENAI_API_KEY")
	return openai.New(strings.TrimSpace(key), c.Model)
}
