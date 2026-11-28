package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/vichanse/augustin/internal/model"
)

// Scripted token usage per model response, so reports exercise real counting.
const scriptedInputTokens, scriptedOutputTokens = 100, 20

type step = model.Response

func toolStep(name string, args any) step {
	raw, _ := json.Marshal(args)
	return step{FinishReason: model.FinishToolCalls, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{Name: name, Arguments: raw}}}}
}

func read(path string) step { return toolStep("read_file", map[string]string{"path": path}) }

func edit(path, old, new string) step {
	return toolStep("edit_file", map[string]string{"path": path, "old_string": old, "new_string": new})
}

func say(text string) step {
	return step{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: text}}
}

// Scenario is a scripted model behaviour for one case.
type Scenario struct {
	Case   string
	Name   string
	Expect Outcome
	Steps  []step
}

// Scenarios are deterministic: no network and no API key. References fix
// the bug in a way the hidden tests accept; the others must not resolve.
var Scenarios = []Scenario{
	{"discount", "reference", Resolved, []step{read("discount.go"),
		edit("discount.go", "return price - price*discount/100", "return price - discount"), say("Fixed the discount.")}},
	{"discount", "wrong-fix", Unresolved, []step{read("discount.go"),
		edit("discount.go", "return price - price*discount/100", "return price - discount*2"), say("Fixed.")}},
	{"discount", "false-claim", Unresolved, []step{read("discount.go"), say("I fixed the bug and all tests pass.")}},
	{"pagination", "reference", Resolved, []step{read("page.go"),
		edit("page.go", "start := page * size", "start := (page - 1) * size"), say("Fixed the page offset.")}},
	{"pagination", "edit-test", Unresolved, []step{read("page_test.go"),
		edit("page_test.go", `want := []string{"a", "b"}`, `want := []string{"c", "d"}`), say("Adjusted the test.")}},
	{"nil-pointer", "reference", Resolved, []step{read("profile.go"),
		edit("profile.go", `	return "Hello, " + u.Name + " from " + u.Address.City`, `	if u.Address == nil || u.Address.City == "" {
		return "Hello, " + u.Name
	}
	return "Hello, " + u.Name + " from " + u.Address.City`), say("Handled the missing address.")}},
	{"multi-file", "reference", Resolved, []step{toolStep("workspace_overview", map[string]string{}), read("order.go"), read("shipping.go"),
		edit("shipping.go", "subtotal > freeShippingFrom", "subtotal >= freeShippingFrom"), say("Free shipping now starts at 100.")}},
	{"regression-trap", "reference", Resolved, []step{read("slug.go"),
		edit("slug.go", `	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return b.String()`, `	var b strings.Builder
	separator := false
	for _, r := range strings.ToLower(s) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			separator = true
			continue
		}
		if separator && b.Len() > 0 {
			b.WriteByte('-')
		}
		separator = false
		b.WriteRune(r)
	}
	return b.String()`), say("Slugs now collapse separators.")}},
	{"regression-trap", "superficial", Unresolved, []step{read("slug.go"),
		edit("slug.go", "return b.String()", `return strings.Trim(strings.ReplaceAll(b.String(), "--", "-"), "-")`), say("Fixed the visible example.")}},
	{"smoke-readonly", "reference", Resolved, []step{read("discount.go"), say("FinalPrice adds the percentage instead of subtracting it.")}},
}

// DeterministicJobs returns the scripted scenarios for the given cases.
func DeterministicJobs(cases []Case) []Job {
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	var jobs []Job
	for _, s := range Scenarios {
		if c, ok := byID[s.Case]; ok {
			jobs = append(jobs, Job{Case: c, Scenario: s.Name, Expect: s.Expect})
		}
	}
	return jobs
}

// ScriptedProvider plays a scenario. It never contacts a model provider.
func ScriptedProvider(t Target) (model.Provider, error) {
	for _, s := range Scenarios {
		if s.Case == t.Case.ID && s.Name == t.Scenario {
			return &scripted{steps: s.Steps}, nil
		}
	}
	return nil, fmt.Errorf("no scripted scenario %s/%s", t.Case.ID, t.Scenario)
}

type scripted struct {
	mu    sync.Mutex
	steps []step
	next  int
}

// Complete returns the next step, then "Done." once the script ends (for
// example after the harness ran the tests). Tool call IDs are unique.
func (s *scripted) Complete(_ context.Context, _ model.Request) (model.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := say("Done.")
	if s.next < len(s.steps) {
		resp = s.steps[s.next]
		resp.Message.ToolCalls = append([]model.ToolCall(nil), resp.Message.ToolCalls...)
		for i := range resp.Message.ToolCalls {
			resp.Message.ToolCalls[i].ID = fmt.Sprintf("call-%d-%d", s.next, i)
		}
	}
	s.next++
	resp.Usage = model.Usage{InputTokens: scriptedInputTokens, OutputTokens: scriptedOutputTokens}
	return resp, nil
}
