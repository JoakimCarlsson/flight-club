package httpx

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/chat"
	"github.com/joakimcarlsson/minmux/openapi"
	"github.com/joakimcarlsson/minmux/router"
)

// chatTurnDTO is one message of the conversation.
type chatTurnDTO struct {
	Role string `json:"role" desc:"user or assistant."`
	Text string `json:"text" desc:"The message."`
}

// chatRequest is the conversation so far, the last turn being the new user
// message. Conversation memory lives with the client.
type chatRequest struct {
	Messages []chatTurnDTO `json:"messages" desc:"The conversation so far; the last entry is the new user message."`
}

// chatParams binds the chat body.
type chatParams struct {
	Body chatRequest `body:""`
}

// chatEventDTO is one frame of the stream. The event name says which fields
// mean anything.
type chatEventDTO struct {
	Text    string           `json:"text,omitempty"    desc:"On text: a piece of the reply to append. On done: the whole reply."`
	CallID  string           `json:"callId,omitempty"  desc:"On tool, toolResult, search, step and results: the tool call it belongs to."`
	Tool    string           `json:"tool,omitempty"    desc:"On tool and toolResult: the tool's name."`
	Input   string           `json:"input,omitempty"   desc:"On tool and toolResult: the arguments the model chose, as JSON."`
	Failed  bool             `json:"failed,omitempty"  desc:"On toolResult: the tool reported an error."`
	Search  *chat.SearchView `json:"search,omitempty"  desc:"On search: what the assistant understood the traveller to want."`
	Step    *award.Step      `json:"step,omitempty"    desc:"On step: one program-day starting or finishing."`
	Results *award.Result    `json:"results,omitempty" desc:"On results: every offer the search found."`
	Error   string           `json:"error,omitempty"   desc:"On error: what went wrong."`
}

// registerChat registers the chat endpoint together with its OpenAPI
// description.
func (s *Server) registerChat() {
	s.router.Post(
		"/api/chat/stream",
		s.chatStream,
		openapi.Tags("Chat"),
		openapi.Summary("Talk to the award assistant, watching it work"),
		openapi.Description(
			"The turn, delivered over Server-Sent Events as it happens. Event names: `text` is a piece of the reply; `tool` announces a tool call and `toolResult` closes it; `search` is the search the assistant understood; `step` is one program-day starting or finishing; `results` is everything a search found; `done` carries the whole reply and is always last; `error` replaces it when the turn failed.",
		),
		openapi.SSEStream[chatEventDTO](
			http.StatusOK,
			"The turn, as it happens.",
		),
		openapi.ReturnsBody[router.ProblemDetails](
			http.StatusBadRequest, "There is no user message to answer."),
		openapi.ReturnsBody[router.ProblemDetails](
			http.StatusServiceUnavailable, "No LLM is configured."),
	)
}

// chatStream handles POST /api/chat/stream: it answers the conversation's
// last message, streaming every event of the turn. Before the stream opens,
// failures are JSON problems; after it, they are an error event, since the
// status is already sent.
func (s *Server) chatStream(c *router.Context, p chatParams) {
	if s.chat == nil {
		c.JSON(http.StatusServiceUnavailable, &router.ProblemDetails{
			Status: http.StatusServiceUnavailable,
			Title:  http.StatusText(http.StatusServiceUnavailable),
			Detail: "no LLM is configured; set OPENROUTER_API_KEY",
		})
		return
	}
	history := make([]chat.Turn, 0, len(p.Body.Messages))
	for _, m := range p.Body.Messages {
		if text := strings.TrimSpace(m.Text); text != "" {
			history = append(history, chat.Turn{Role: m.Role, Text: text})
		}
	}
	if len(history) == 0 || history[len(history)-1].Role != "user" {
		c.JSON(http.StatusBadRequest,
			router.BadRequest("the last message must be the user's"))
		return
	}

	sse := c.SSE(http.StatusOK)
	defer func() { _ = sse.Close() }()

	reply, err := s.chat.Reply(c.Ctx(), history, func(ev chat.Event) {
		_ = sse.Send(router.SSEEvent{
			Event: string(ev.Kind),
			Data: chatEventDTO{
				Text:    ev.Text,
				CallID:  ev.CallID,
				Tool:    ev.Tool,
				Input:   ev.Input,
				Failed:  ev.Failed,
				Search:  ev.Search,
				Step:    ev.Step,
				Results: ev.Results,
			},
		})
	})
	if err != nil {
		if !errors.Is(err, c.Ctx().Err()) {
			slog.ErrorContext(c.Ctx(), "chat turn failed", "error", err)
		}
		_ = sse.Send(router.SSEEvent{
			Event: "error",
			Data:  chatEventDTO{Error: "the assistant failed to reply"},
		})
		return
	}
	_ = sse.Send(
		router.SSEEvent{Event: "done", Data: chatEventDTO{Text: reply}},
	)
}
