// Package approval coordinates interactive decisions for sensitive tool calls.
//
// The permission policy in package tools stays the enforcement authority: it
// decides whether an action may be approved at all and calls Broker.Approve.
// The Broker only relays one pending request to a UI and waits for the user's
// decision, the request context, or Close. It never grants anything by itself.
package approval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"unicode/utf8"

	"github.com/vichanse/augustin/internal/tools"
)

var (
	// ErrClosed means the UI went away; pending and later requests fail closed.
	ErrClosed = errors.New("approval session closed")
	// ErrBusy rejects a request made while another one is still pending.
	ErrBusy = errors.New("another approval is pending")
)

const (
	maxToolBytes   = 64
	maxActionBytes = 4096
	maxPathBytes   = 1024
	maxDiffBytes   = 4096 // tools rejects larger diffs; this is defense in depth
)

// Request is an immutable snapshot of one proposed action. Its text fields
// come from the model: renderers must sanitize them before display.
type Request struct {
	ID         uint64
	Capability tools.Capability
	Tool       string
	Action     string // compact JSON arguments, bounded
	Path       string // target of a prepared edit, if any
	Diff       string // unified diff of a prepared edit; shown instead of Action
	Truncated  bool   // a field is incomplete: the request can only be denied
}

// Update tells the UI that a request opened, or closed without a decision
// (canceled, deadline or session close). Notify must not block for long.
type Update struct {
	Request Request
	Open    bool
}

type pending struct {
	req   Request
	key   [32]byte
	reply chan bool
}

type Broker struct {
	mu       sync.Mutex
	notify   func(Update)
	nextID   uint64
	current  *pending
	rejected map[[32]byte]bool
	closed   bool
	done     chan struct{}
}

func New() *Broker {
	return &Broker{rejected: make(map[[32]byte]bool), done: make(chan struct{})}
}

// Attach sets the UI callback. Without one, every request fails closed.
func (b *Broker) Attach(notify func(Update)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.notify = notify
}

// Approve implements tools.Approver. It returns (true, nil) only for an
// explicit approval of this exact request.
func (b *Broker) Approve(ctx context.Context, proposal tools.Proposal) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	key := fingerprint(proposal)
	b.mu.Lock()
	switch {
	case b.closed || b.notify == nil:
		b.mu.Unlock()
		return false, ErrClosed
	case b.rejected[key]:
		// Already refused in this session: answer again without re-prompting.
		b.mu.Unlock()
		return false, nil
	case b.current != nil:
		b.mu.Unlock()
		return false, ErrBusy
	}
	b.nextID++
	p := &pending{req: snapshot(b.nextID, proposal), key: key, reply: make(chan bool, 1)}
	b.current = p
	notify := b.notify
	b.mu.Unlock()

	notify(Update{Request: p.req, Open: true})
	select {
	case approved := <-p.reply:
		if ctx.Err() != nil {
			return false, ctx.Err() // a decision racing a cancellation never authorizes
		}
		return approved, nil
	case <-ctx.Done():
		b.withdraw(p)
		return false, ctx.Err()
	case <-b.done:
		return false, ErrClosed
	}
}

// Resolve applies a decision to the pending request with this ID only.
// It reports false for stale, duplicate or unknown decisions, and refuses to
// approve a truncated request: approval requires the complete action.
func (b *Broker) Resolve(id uint64, approved bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.current
	if p == nil || p.req.ID != id || (approved && p.req.Truncated) {
		return false
	}
	b.current = nil
	if !approved {
		b.rejected[p.key] = true
	}
	p.reply <- approved // buffered: never blocks
	return true
}

// Pending returns the request awaiting a decision, if any.
func (b *Broker) Pending() (Request, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.current == nil {
		return Request{}, false
	}
	return b.current.req, true
}

// Close releases any waiting request and fails every later one.
func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.current = nil
	close(b.done)
}

func (b *Broker) withdraw(p *pending) {
	b.mu.Lock()
	if b.current != p {
		b.mu.Unlock()
		return
	}
	b.current = nil
	notify := b.notify
	b.mu.Unlock()
	if notify != nil {
		notify(Update{Request: p.req})
	}
}

func snapshot(id uint64, p tools.Proposal) Request {
	action := string(p.Call.Arguments)
	var compact bytes.Buffer
	if json.Compact(&compact, p.Call.Arguments) == nil {
		action = compact.String()
	}
	tool, toolCut := bound(p.Call.Name, maxToolBytes)
	action, actionCut := bound(action, maxActionBytes)
	path, pathCut := bound(p.Path, maxPathBytes)
	diff, diffCut := bound(p.Diff, maxDiffBytes)
	return Request{ID: id, Capability: p.Capability, Tool: tool, Action: action, Path: path, Diff: diff,
		Truncated: toolCut || actionCut || pathCut || diffCut}
}

// bound cuts s to at most n bytes on a rune boundary.
func bound(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// fingerprint identifies an action for rejection memory only. Compaction
// removes whitespace outside strings, so it can only widen what counts as
// already refused; it never changes what executes.
// The diff is included so the same arguments against changed content ask again.
func fingerprint(p tools.Proposal) [32]byte {
	args := p.Call.Arguments
	var compact bytes.Buffer
	if json.Compact(&compact, args) == nil {
		args = compact.Bytes()
	}
	h := sha256.New()
	for _, part := range [][]byte{[]byte(p.Capability), []byte(p.Call.Name), args, []byte(p.Path), []byte(p.Diff)} {
		h.Write([]byte{byte(len(part) >> 24), byte(len(part) >> 16), byte(len(part) >> 8), byte(len(part))})
		h.Write(part)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
