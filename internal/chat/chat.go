// Package chat is the conversational surface over the award search. The
// agent is agent.Agent from github.com/joakimcarlsson/ai; this package holds
// what is ours: the system prompt, the search tool, the conversation as a
// throwaway seeded session, and the translation of everything that happens
// during a turn into the events the browser draws.
//
// Conversation memory lives with the client. Each turn is produced from the
// full history the caller sends.
package chat

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/joakimcarlsson/ai/agent"
	"github.com/joakimcarlsson/ai/llm"
	"github.com/joakimcarlsson/ai/message"
	"github.com/joakimcarlsson/ai/session"
	"github.com/joakimcarlsson/ai/types"

	"github.com/joakimcarlsson/flight-club/internal/award"
)

// systemTemplate is the system prompt, with {{TODAY}} for today's date.
//
//go:embed prompts/system.md
var systemTemplate string

// maxIterations bounds the model round trips of one turn, so a model that
// keeps calling the tool cannot run a turn forever.
const maxIterations = 6

// sessionID names the throwaway session a turn's history is seeded into.
// One name is enough because every turn gets its own store.
const sessionID = "turn"

// Turn is one message of the conversation.
type Turn struct {
	// Role is "user" or "assistant".
	Role string
	// Text is the message.
	Text string
}

// EventKind names what happened. It is the SSE event name the browser
// switches on, so these strings are part of the wire contract.
type EventKind string

// The kinds of event a turn produces.
const (
	// EventText is a piece of the reply as the model writes it.
	EventText EventKind = "text"
	// EventTool announces a tool call with the arguments the model chose.
	EventTool EventKind = "tool"
	// EventToolResult closes a tool call.
	EventToolResult EventKind = "toolResult"
	// EventSearch is the search the tool is about to run, as understood.
	EventSearch EventKind = "search"
	// EventStep is one program-day of a search starting or finishing.
	EventStep EventKind = "step"
	// EventResults is everything a search found.
	EventResults EventKind = "results"
)

// Event is one thing that happened during a turn. Kind says which of the
// other fields carry anything.
type Event struct {
	// Kind names the event.
	Kind EventKind
	// Text is a piece of the reply, for EventText.
	Text string
	// CallID correlates a tool call across its events.
	CallID string
	// Tool is the tool's name, for EventTool and EventToolResult.
	Tool string
	// Input is the arguments the model chose, as JSON.
	Input string
	// Failed marks a tool call that reported an error.
	Failed bool
	// Search is the search being run, for EventSearch.
	Search *SearchView
	// Step is the program-day that moved, for EventStep.
	Step *award.Step
	// Results is what a search found, for EventResults.
	Results *award.Result
}

// Emit receives a turn's events in order. It must not block for long.
type Emit func(Event)

// Assistant answers turns with an LLM that can search award availability.
type Assistant struct {
	llm    llm.LLM
	awards *award.Searcher
	now    func() time.Time
}

// New builds an Assistant that thinks with model and searches with awards.
func New(model llm.LLM, awards *award.Searcher) *Assistant {
	return &Assistant{llm: model, awards: awards, now: time.Now}
}

// ErrNoMessage is returned when the conversation has no user message to
// answer.
var ErrNoMessage = errors.New("chat: no message to reply to")

// Reply answers the last turn of history, which must be the user's,
// reporting every event to emit as it happens, and returns the finished
// reply. It returns ErrNoMessage when history does not end with a user turn,
// and an error when the model fails.
func (a *Assistant) Reply(
	ctx context.Context,
	history []Turn,
	emit Emit,
) (string, error) {
	if len(history) == 0 || history[len(history)-1].Role != "user" {
		return "", ErrNoMessage
	}
	if emit == nil {
		emit = func(Event) {}
	}

	store := session.MemoryStore()
	sess, err := store.Create(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("chat: session: %w", err)
	}
	if prior := history[:len(history)-1]; len(prior) > 0 {
		msgs := make([]message.Message, 0, len(prior))
		for _, t := range prior {
			msgs = append(msgs, a.message(t))
		}
		if err := sess.AddMessages(ctx, msgs); err != nil {
			return "", fmt.Errorf("chat: session: %w", err)
		}
	}

	ag := agent.New(a.llm,
		agent.WithSystemPrompt(a.systemPrompt()),
		agent.WithSession(sessionID, store),
		agent.WithTools(&searchTool{awards: a.awards, emit: emit}),
		agent.WithMaxIterations(maxIterations),
	)
	return stream(ctx, ag, history[len(history)-1].Text, emit)
}

// systemPrompt is the system prompt with today's date filled in.
func (a *Assistant) systemPrompt() string {
	return strings.ReplaceAll(
		systemTemplate,
		"{{TODAY}}",
		a.now().Format("Monday 2 January 2006"),
	)
}

// message turns one turn of history into a model message.
func (a *Assistant) message(t Turn) message.Message {
	if t.Role == "assistant" {
		m := message.NewAssistantMessage()
		m.Model = a.llm.Model().ID
		m.AppendContent(t.Text)
		return m
	}
	m := message.NewUserMessage(t.Text)
	m.Model = a.llm.Model().ID
	return m
}

// stream runs text on ag, reporting its text and tool calls to emit, and
// returns the finished reply. The event channel is drained to the end on
// every path, since abandoning it leaks the provider's goroutines. It
// returns the first error the stream reports, or an error when it ends
// without a response.
func stream(
	ctx context.Context,
	ag *agent.Agent,
	text string,
	emit Emit,
) (string, error) {
	var (
		reply     string
		completed bool
		streamErr error
	)
	for ev := range ag.ChatStream(ctx, text) {
		switch ev.Type {
		case types.EventContentDelta:
			if ev.Content != "" {
				emit(Event{Kind: EventText, Text: ev.Content})
			}
		case types.EventToolUseStart:
			if ev.ToolCall != nil {
				emit(Event{
					Kind:   EventTool,
					CallID: ev.ToolCall.ID,
					Tool:   ev.ToolCall.Name,
					Input:  ev.ToolCall.Input,
				})
			}
		case types.EventToolUseStop:
			if ev.ToolResult != nil {
				emit(Event{
					Kind:   EventToolResult,
					CallID: ev.ToolResult.ToolCallID,
					Tool:   ev.ToolResult.ToolName,
					Input:  ev.ToolResult.Input,
					Failed: ev.ToolResult.IsError,
				})
			}
		case types.EventComplete:
			completed = true
			if ev.Response != nil {
				reply = ev.Response.Content
			}
		case types.EventError:
			if streamErr == nil {
				streamErr = ev.Error
			}
		}
	}
	switch {
	case streamErr != nil:
		return "", fmt.Errorf("chat: %w", streamErr)
	case !completed:
		return "", errors.New("chat: stream ended without a response")
	}
	return reply, nil
}
