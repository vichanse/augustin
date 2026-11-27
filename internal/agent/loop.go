// Package agent implements the bounded model/tool execution loop.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/vichanse/augustin/internal/events"
	"github.com/vichanse/augustin/internal/model"
	"github.com/vichanse/augustin/internal/tools"
	"github.com/vichanse/augustin/internal/verification"
)

var ErrIterationLimit = errors.New("agent iteration limit reached")
var ErrBudgetExceeded = errors.New("execution budget exceeded")
var ErrInvalidResponse = errors.New("invalid model response")

// Executor must enforce authorization independently of model instructions.
type Executor interface {
	Execute(ctx context.Context, call model.ToolCall) (string, error)
}

type Loop struct {
	Provider             model.Provider
	Executor             Executor
	MaxIterations        int
	Events               events.Sink
	Budget               Budget
	ReserveFinalResponse bool
	// Verification, when set, receives registry evidence for the turn and
	// gates completion: see verificationGate.
	Verification *verification.Tracker
}

// maxVerificationContinuations bounds how often one turn is sent back for
// verification; continuations still consume every other budget.
const maxVerificationContinuations = 2

// Budget bounds tool execution and cooperative run duration.
// Zero values select conservative defaults.
type Budget struct {
	MaxToolCalls   int
	MaxOutputBytes int
	Timeout        time.Duration
}

type Result struct {
	Messages    []model.Message
	Iterations  int
	ToolCalls   int
	OutputBytes int
	// Verification is the harness verdict, also set when the run fails.
	Verification verification.Report
}

func (l Loop) Run(ctx context.Context, req model.Request) (result Result, runErr error) {
	result = Result{Messages: append([]model.Message(nil), req.Messages...)}
	sequence := 0
	emit := func(e events.Event) {
		if l.Events != nil {
			sequence++
			e.Sequence = sequence
			e.Iteration = result.Iterations
			l.Events.Emit(e)
		}
	}
	emit(events.Event{Kind: events.AgentStarted})
	defer func() {
		if l.Verification != nil {
			result.Verification = l.Verification.Report()
		}
		kind := events.AgentFailed
		reason := events.ExecutionError
		switch {
		case runErr == nil:
			kind, reason = events.AgentCompleted, events.Success
		case errors.Is(runErr, context.Canceled):
			reason = events.Canceled
		case errors.Is(runErr, context.DeadlineExceeded):
			reason = events.Deadline
		case errors.Is(runErr, ErrIterationLimit):
			reason = events.IterationLimit
		case errors.Is(runErr, ErrBudgetExceeded):
			reason = events.BudgetExceeded
		case errors.Is(runErr, ErrInvalidResponse):
			reason = events.InvalidModel
		}
		emit(events.Event{Kind: kind, Reason: reason})
	}()
	if l.Provider == nil || l.MaxIterations <= 0 {
		return result, errors.New("provider and positive iteration limit required")
	}
	b := l.Budget
	if b.MaxToolCalls < 0 || b.MaxOutputBytes < 0 || b.Timeout < 0 {
		return result, errors.New("negative execution budget")
	}
	if b.MaxToolCalls == 0 {
		b.MaxToolCalls = 32
	}
	if b.MaxOutputBytes == 0 {
		b.MaxOutputBytes = 2 << 20
	}
	if b.Timeout == 0 {
		b.Timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	continuations := 0
	allowed := make(map[string]bool, len(req.Tools))
	for _, tool := range req.Tools {
		allowed[tool.Name] = true
	}
	for result.Iterations < l.MaxIterations {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		req.Messages = append([]model.Message(nil), result.Messages...)
		final := l.ReserveFinalResponse && result.Iterations == l.MaxIterations-1
		if l.ReserveFinalResponse {
			instruction := fmt.Sprintf("Execution budget: %d model calls remaining including this call. %d tool calls remaining. Avoid repeated exploration; answer as soon as evidence is sufficient.", l.MaxIterations-result.Iterations, b.MaxToolCalls-result.ToolCalls)
			if final {
				req.Tools = nil
				instruction = "Final response required now. No tools are available. Answer using the evidence already collected; explicitly state any gaps or uncertainty. Do not claim unverified findings."
			}
			req.Messages = append(req.Messages, model.Message{Role: model.RoleSystem, Content: instruction})
		}
		result.Iterations++
		emit(events.Event{Kind: events.ModelRequested})
		var response model.Response
		var err error
		stream, canStream := l.Provider.(interface {
			CompleteStream(context.Context, model.Request, func(string)) (model.Response, error)
		})
		sink, wantsStream := l.Events.(interface{ TextDelta(string) })
		if canStream && wantsStream {
			response, err = stream.CompleteStream(ctx, req, sink.TextDelta)
		} else {
			response, err = l.Provider.Complete(ctx, req)
		}
		if err != nil {
			emit(events.Event{Kind: events.ModelFailed})
			return result, fmt.Errorf("model completion: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		emit(events.Event{Kind: events.ModelCompleted, InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens})
		msg := response.Message
		if msg.Role != model.RoleAssistant {
			return result, ErrInvalidResponse
		}
		finishing := false
		switch response.FinishReason {
		case model.FinishLength:
			if len(msg.ToolCalls) != 0 || msg.Content == "" {
				return result, fmt.Errorf("%w: completion token limit reached", ErrInvalidResponse)
			}
			msg.Content += "\n\n[Response truncated: model completion token limit reached.]"
			finishing = true
		case model.FinishStop:
			if len(msg.ToolCalls) != 0 {
				return result, ErrInvalidResponse
			}
			finishing = true
		case model.FinishToolCalls:
			if final {
				return result, ErrIterationLimit
			}
			if len(msg.ToolCalls) == 0 || l.Executor == nil {
				return result, ErrInvalidResponse
			}
		default:
			return result, fmt.Errorf("%w: unsupported finish reason", ErrInvalidResponse)
		}
		if finishing {
			result.Messages = append(result.Messages, msg)
			if instruction := l.verificationGate(ctx, result, final, b, &continuations); instruction != "" {
				result.Messages = append(result.Messages, model.Message{Role: model.RoleSystem, Content: instruction})
				continue
			}
			return result, nil
		}
		if len(msg.ToolCalls) > b.MaxToolCalls-result.ToolCalls {
			return result, fmt.Errorf("%w: tool calls", ErrBudgetExceeded)
		}
		seen := make(map[string]bool)
		for _, call := range msg.ToolCalls {
			if call.ID == "" || seen[call.ID] || !allowed[call.Name] || !json.Valid(call.Arguments) {
				return result, ErrInvalidResponse
			}
			seen[call.ID] = true
		}
		result.Messages = append(result.Messages, msg)
		for _, call := range msg.ToolCalls {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			toolName := safeToolName(call.Name)
			emit(events.Event{Kind: events.ToolRequested, Tool: toolName})
			// Display-only hook: it sees raw arguments, so sinks implementing it must not persist them.
			if display, ok := l.Events.(interface{ ToolCall(model.ToolCall) }); ok {
				display.ToolCall(call)
			}
			result.ToolCalls++
			output, err := l.Executor.Execute(ctx, call)
			if err != nil {
				reason, recoverable := classifyToolError(err)
				emit(events.Event{Kind: events.ToolFailed, Tool: toolName, Reason: reason, Recoverable: recoverable})
				if !recoverable {
					return result, fmt.Errorf("tool execution: %w", err)
				}
				output = "Tool error: " + string(reason)
			}
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if len(output) > b.MaxOutputBytes-result.OutputBytes {
				emit(events.Event{Kind: events.ToolFailed, Tool: toolName, Reason: events.BudgetExceeded})
				return result, fmt.Errorf("%w: tool output", ErrBudgetExceeded)
			}
			result.OutputBytes += len(output)
			if err == nil {
				emit(events.Event{Kind: events.ToolCompleted, Tool: toolName})
			}
			result.Messages = append(result.Messages, model.Message{
				Role: model.RoleTool, Content: output, ToolCallID: call.ID,
			})
		}
	}
	return result, ErrIterationLimit
}

func safeToolName(name string) string {
	switch name {
	case "read_file", "list_files", "workspace_overview", "edit_file", "run_command":
		return name
	default:
		return "other"
	}
}

func classifyToolError(err error) (events.Reason, bool) {
	switch {
	case errors.Is(err, context.Canceled):
		return events.Canceled, false
	case errors.Is(err, context.DeadlineExceeded):
		return events.Deadline, false
	case errors.Is(err, tools.ErrRejected):
		return "user_rejected", true // tell the model, which may explain or propose another way
	case errors.Is(err, tools.ErrDenied), errors.Is(err, tools.ErrSensitivePath):
		return "permission_denied", false
	case errors.Is(err, tools.ErrFileChanged):
		return "file_changed", true
	case errors.Is(err, tools.ErrNoMatch):
		return "old_string_not_found", true
	case errors.Is(err, tools.ErrAmbiguousMatch):
		return "old_string_ambiguous", true
	case errors.Is(err, tools.ErrNoOp):
		return "no_op_edit", true
	case errors.Is(err, tools.ErrNotText):
		return "not_text_file", true
	case errors.Is(err, tools.ErrDiffTooLarge):
		return "diff_too_large", true
	case errors.Is(err, tools.ErrCommandNotAllowed):
		return "command_not_allowed", true
	case errors.Is(err, tools.ErrExecutableNotFound):
		return "executable_not_found", true
	case errors.Is(err, tools.ErrInvalidArguments):
		return "invalid_arguments", true
	case errors.Is(err, tools.ErrDirectoryRequired):
		return "directory_required", true
	case errors.Is(err, tools.ErrRegularFileRequired):
		return "regular_file_required", true
	case errors.Is(err, tools.ErrFileTooLarge):
		return "file_too_large", true
	case errors.Is(err, tools.ErrDirectoryEntryLimit):
		return "directory_entry_limit", true
	case errors.Is(err, fs.ErrPermission):
		return "permission_denied", false
	case errors.Is(err, fs.ErrNotExist):
		return "file_not_found", true
	default:
		return events.ExecutionError, false
	}
}

// verificationGate decides whether a model that tries to finish is sent back
// to obtain verification. It only continues while a non-final iteration and
// a tool call remain for the test run plus a later answer, so the reserved
// final response never gets tools. Otherwise it records why verification is
// incomplete and lets the turn finish with the harness verdict.
func (l Loop) verificationGate(ctx context.Context, result Result, final bool, b Budget, used *int) string {
	if l.Verification == nil {
		return ""
	}
	instruction := l.Verification.Continuation()
	switch {
	case instruction == "":
		return ""
	case *used >= maxVerificationContinuations:
		l.Verification.MarkIncomplete("verification retries exhausted")
	case final || result.Iterations > l.MaxIterations-2 || result.ToolCalls >= b.MaxToolCalls || ctx.Err() != nil:
		l.Verification.MarkIncomplete("execution budget exhausted")
	default:
		*used++
		return instruction
	}
	return ""
}
