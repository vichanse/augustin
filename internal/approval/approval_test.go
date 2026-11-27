package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vichanse/augustin/internal/agent"
	"github.com/vichanse/augustin/internal/approval"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/model/modeltest"
	"github.com/vichanse/augustin/internal/tools"
)

// recorder is a fake UI: it records updates and never blocks.
type recorder struct {
	mu      sync.Mutex
	updates []approval.Update
}

func (r *recorder) notify(u approval.Update) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, u)
}

func (r *recorder) opened() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, u := range r.updates {
		if u.Open {
			n++
		}
	}
	return n
}

func newBroker() (*approval.Broker, *recorder) {
	b, r := approval.New(), &recorder{}
	b.Attach(r.notify)
	return b, r
}

func waitPending(t *testing.T, b *approval.Broker) approval.Request {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if req, ok := b.Pending(); ok {
			return req
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no pending approval")
	return approval.Request{}
}

type result struct {
	ok  bool
	err error
}

func approveAsync(b *approval.Broker, ctx context.Context, args string) <-chan result {
	out := make(chan result, 1)
	go func() {
		ok, err := b.Approve(ctx, tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "fake_write", Arguments: json.RawMessage(args)}})
		out <- result{ok, err}
	}()
	return out
}

func wait(t *testing.T, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("approval goroutine was not released")
		return result{}
	}
}

// fakeWrite is a sensitive test-only tool; it is never offered to a real model.
func fakeWrite(count *atomic.Int32) map[string]tools.Handler {
	return map[string]tools.Handler{"fake_write": func(context.Context, model.ToolCall) (string, error) {
		count.Add(1)
		return "written", nil
	}}
}

func editRegistry(count *atomic.Int32, b *approval.Broker) *tools.Registry {
	return tools.NewWithPolicy(fakeWrite(count), []string{"fake_write"}, tools.Policy{
		Mode: tools.EditWithApproval, Capabilities: map[string]tools.Capability{"fake_write": tools.Edit}, Approve: b.Approve,
	})
}

func writeCall(id string) model.Response {
	return model.Response{FinishReason: model.FinishToolCalls, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{
		{ID: id, Name: "fake_write", Arguments: json.RawMessage(`{"path":"a.txt"}`)},
	}}}
}

func stop(text string) model.Response {
	return model.Response{FinishReason: model.FinishStop, Message: model.Message{Role: model.RoleAssistant, Content: text}}
}

func runLoop(t *testing.T, exec agent.Executor, steps ...modeltest.Step) (agent.Result, error) {
	t.Helper()
	l := agent.Loop{Provider: modeltest.New(steps...), Executor: exec, MaxIterations: len(steps)}
	return l.Run(context.Background(), model.Request{Tools: []model.ToolDefinition{{Name: "fake_write"}}})
}

func TestApprovedActionExecutesExactlyOnce(t *testing.T) {
	var count atomic.Int32
	b, _ := newBroker()
	go func() {
		req := waitPending(t, b)
		if !b.Resolve(req.ID, true) {
			t.Error("valid decision refused")
		}
		if b.Resolve(req.ID, true) {
			t.Error("duplicate decision accepted")
		}
	}()
	r, err := runLoop(t, editRegistry(&count, b), modeltest.Step{Response: writeCall("1")}, modeltest.Step{Response: stop("done")})
	if err != nil || count.Load() != 1 || r.Messages[1].Content != "written" {
		t.Fatalf("approved action: count=%d err=%v", count.Load(), err)
	}
}

func TestRejectedActionNeverExecutesAndIsNotPromptedAgain(t *testing.T) {
	var count atomic.Int32
	b, rec := newBroker()
	go func() {
		req := waitPending(t, b)
		b.Resolve(req.ID, false)
	}()
	// The model retries the identical action: it must be refused without a second prompt.
	r, err := runLoop(t, editRegistry(&count, b),
		modeltest.Step{Response: writeCall("1")}, modeltest.Step{Response: writeCall("2")}, modeltest.Step{Response: stop("I will not write it")})
	if err != nil || count.Load() != 0 {
		t.Fatalf("rejected action: count=%d err=%v", count.Load(), err)
	}
	if rec.opened() != 1 {
		t.Fatalf("identical rejected action prompted %d times", rec.opened())
	}
	if r.Messages[1].Content != "Tool error: user_rejected" || r.Messages[3].Content != "Tool error: user_rejected" {
		t.Fatalf("rejection not reported to the model as recoverable: %+v", r.Messages)
	}
}

func TestCancellationReleasesPendingApproval(t *testing.T) {
	b, rec := newBroker()
	ctx, cancel := context.WithCancel(context.Background())
	ch := approveAsync(b, ctx, `{}`)
	req := waitPending(t, b)
	cancel()
	if r := wait(t, ch); r.ok || !errors.Is(r.err, context.Canceled) {
		t.Fatalf("canceled approval: %+v", r)
	}
	if _, ok := b.Pending(); ok || b.Resolve(req.ID, true) {
		t.Fatal("canceled request still pending")
	}
	rec.mu.Lock()
	last := rec.updates[len(rec.updates)-1]
	rec.mu.Unlock()
	if last.Open || last.Request.ID != req.ID {
		t.Fatal("UI not told the request was withdrawn")
	}
}

func TestDeadlineDeniesAction(t *testing.T) {
	var count atomic.Int32
	b, _ := newBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := editRegistry(&count, b).Execute(ctx, model.ToolCall{Name: "fake_write", Arguments: json.RawMessage(`{}`)})
	if !errors.Is(err, context.DeadlineExceeded) || count.Load() != 0 {
		t.Fatalf("deadline: count=%d err=%v", count.Load(), err)
	}
}

func TestStaleDecisionCannotAuthorizeAnotherRequest(t *testing.T) {
	b, _ := newBroker()
	ctx, cancel := context.WithCancel(context.Background())
	first := approveAsync(b, ctx, `{"n":1}`)
	stale := waitPending(t, b)
	cancel()
	wait(t, first)

	second := approveAsync(b, context.Background(), `{"n":2}`)
	current := waitPending(t, b)
	if current.ID == stale.ID {
		t.Fatal("request IDs reused")
	}
	if b.Resolve(stale.ID, true) {
		t.Fatal("stale decision accepted")
	}
	if _, ok := b.Pending(); !ok {
		t.Fatal("stale decision resolved the current request")
	}
	b.Resolve(current.ID, false)
	if r := wait(t, second); r.ok || r.err != nil {
		t.Fatalf("current request: %+v", r)
	}
}

func TestCloseReleasesPendingAndLaterRequests(t *testing.T) {
	b, _ := newBroker()
	ch := approveAsync(b, context.Background(), `{}`)
	waitPending(t, b)
	b.Close()
	if r := wait(t, ch); r.ok || !errors.Is(r.err, approval.ErrClosed) {
		t.Fatalf("close: %+v", r)
	}
	if r := wait(t, approveAsync(b, context.Background(), `{}`)); r.ok || !errors.Is(r.err, approval.ErrClosed) {
		t.Fatalf("request after close: %+v", r)
	}
	b.Close() // idempotent
}

func TestConcurrentRequestIsRejected(t *testing.T) {
	b, _ := newBroker()
	first := approveAsync(b, context.Background(), `{"n":1}`)
	req := waitPending(t, b)
	if r := wait(t, approveAsync(b, context.Background(), `{"n":2}`)); r.ok || !errors.Is(r.err, approval.ErrBusy) {
		t.Fatalf("concurrent request: %+v", r)
	}
	b.Resolve(req.ID, true)
	if r := wait(t, first); !r.ok || r.err != nil {
		t.Fatalf("first request disturbed: %+v", r)
	}
}

func TestNoUIFailsClosed(t *testing.T) {
	ok, err := approval.New().Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "fake_write"}})
	if ok || !errors.Is(err, approval.ErrClosed) {
		t.Fatalf("headless approval: %v %v", ok, err)
	}
}

func TestRequestIsBoundedImmutableSnapshot(t *testing.T) {
	b, _ := newBroker()
	args := []byte(`{"path": "a.txt",   "content": "` + strings.Repeat("é", 4000) + `"}`)
	go b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "fake_write", Arguments: args}})
	req := waitPending(t, b)
	copy(args, `{"path": "evil"`) // mutating the caller's buffer must not change what is shown
	if !strings.HasPrefix(req.Action, `{"path":"a.txt","content":"`) || !req.Truncated || len(req.Action) > 4096 || !json.Valid([]byte(req.Action+`"}`)) {
		t.Fatalf("snapshot not compact, bounded or valid UTF-8: truncated=%v len=%d", req.Truncated, len(req.Action))
	}
	again, _ := b.Pending()
	if again.Action != req.Action {
		t.Fatal("snapshot changed after caller mutation")
	}
	b.Close()
}

func TestReadOnlyToolsNeedNoApproval(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := tools.OpenWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	b, rec := newBroker()
	policy := tools.ReadPolicy()
	policy.Approve = b.Approve
	r := tools.NewWithPolicy(w.Handlers(), []string{"read_file"}, policy)
	out, err := r.Execute(context.Background(), model.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)})
	if err != nil || out != "package main" || rec.opened() != 0 {
		t.Fatalf("read-only tool: out=%q err=%v prompts=%d", out, err, rec.opened())
	}
}

func TestTruncatedRequestCanOnlyBeDenied(t *testing.T) {
	b, _ := newBroker()
	ch := approveAsync(b, context.Background(), `{"content":"`+strings.Repeat("x", 5000)+`"}`)
	req := waitPending(t, b)
	if !req.Truncated {
		t.Fatal("oversized action not marked truncated")
	}
	if b.Resolve(req.ID, true) {
		t.Fatal("broker approved an action the user could not fully see")
	}
	if _, ok := b.Pending(); !ok {
		t.Fatal("refused approval dropped the request")
	}
	if !b.Resolve(req.ID, false) {
		t.Fatal("deny refused")
	}
	if r := wait(t, ch); r.ok || r.err != nil {
		t.Fatalf("truncated request: %+v", r)
	}
}

func TestRejectionMemoryIgnoresInsignificantWhitespace(t *testing.T) {
	b, rec := newBroker()
	ch := approveAsync(b, context.Background(), `{"path":"a.txt"}`)
	b.Resolve(waitPending(t, b).ID, false)
	wait(t, ch)
	if r := wait(t, approveAsync(b, context.Background(), "{ \"path\" :\n \"a.txt\" }")); r.ok || r.err != nil || rec.opened() != 1 {
		t.Fatalf("whitespace variant re-prompted: %+v prompts=%d", r, rec.opened())
	}
	// Whitespace inside strings is meaningful and must still be asked about.
	go b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "fake_write", Arguments: json.RawMessage(`{"path":"a .txt"}`)}})
	waitPending(t, b)
	b.Close()
}

func TestTruncatedToolNameCanOnlyBeDenied(t *testing.T) {
	b, _ := newBroker()
	go b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: strings.Repeat("t", 100), Arguments: json.RawMessage(`{}`)}})
	req := waitPending(t, b)
	if !req.Truncated || len(req.Tool) > 64 {
		t.Fatalf("cut tool name not flagged: truncated=%v len=%d", req.Truncated, len(req.Tool))
	}
	if b.Resolve(req.ID, true) {
		t.Fatal("broker approved an incomplete tool identity")
	}
	if !b.Resolve(req.ID, false) {
		t.Fatal("deny refused")
	}
	b.Close()
}

func TestEditProposalSnapshotCarriesPathAndDiff(t *testing.T) {
	b, rec := newBroker()
	diff := "--- a/f.go\n+++ b/f.go\n@@ -1,1 +1,1 @@\n-a\n+b"
	ch := make(chan result, 1)
	go func() {
		ok, err := b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "edit_file", Arguments: json.RawMessage(`{}`)}, Path: "f.go", Diff: diff})
		ch <- result{ok, err}
	}()
	req := waitPending(t, b)
	if req.Path != "f.go" || req.Diff != diff || req.Truncated {
		t.Fatalf("snapshot: %+v", req)
	}
	b.Resolve(req.ID, false)
	wait(t, ch)
	// Same arguments against changed content is a different action: ask again.
	go b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "edit_file", Arguments: json.RawMessage(`{}`)}, Path: "f.go", Diff: diff + "\n+c"})
	waitPending(t, b)
	if rec.opened() != 2 {
		t.Fatal("a different diff was refused from memory")
	}
	b.Close()
}

func TestOversizedDiffCanOnlyBeDenied(t *testing.T) {
	b, _ := newBroker()
	go b.Approve(context.Background(), tools.Proposal{Capability: tools.Edit, Call: model.ToolCall{Name: "edit_file", Arguments: json.RawMessage(`{}`)}, Path: "f.go", Diff: strings.Repeat("+x\n", 2000)})
	req := waitPending(t, b)
	if !req.Truncated || b.Resolve(req.ID, true) {
		t.Fatal("oversized diff approvable")
	}
	b.Close()
}
