// Package llmc builds the LLM client the process talks to, on OpenRouter
// through github.com/joakimcarlsson/ai.
package llmc

import (
	"errors"
	"time"

	"github.com/joakimcarlsson/ai/llm"
	llmopenai "github.com/joakimcarlsson/ai/llm/openai"
	llmopenrouter "github.com/joakimcarlsson/ai/llm/openrouter"
)

// provider is the provider name custom models are declared under.
const provider = "openrouter"

// contextWindow is declared for a model the library's catalogue does not
// list.
const contextWindow = 128_000

// timeout bounds one completion.
const timeout = 2 * time.Minute

// Config is the OpenRouter account and model to use.
type Config struct {
	// APIKey is the OpenRouter API key.
	APIKey string
	// Model is an OpenRouter model id, e.g. "z-ai/glm-5.3".
	Model string
}

// ErrNotConfigured is returned by New when no API key is set.
var ErrNotConfigured = errors.New("llmc: OPENROUTER_API_KEY is not set")

// New builds an OpenRouter client for cfg. It returns ErrNotConfigured when
// cfg has no API key, and an error when it has no model.
func New(cfg Config) (llm.LLM, error) {
	if cfg.APIKey == "" {
		return nil, ErrNotConfigured
	}
	if cfg.Model == "" {
		return nil, errors.New(
			"llmc: OPENROUTER_MODEL is not set, e.g. z-ai/glm-5.3",
		)
	}
	return llmopenrouter.NewLLM(
		llmopenai.WithAPIKey(cfg.APIKey),
		llmopenai.WithModel(model(cfg.Model)),
		llmopenai.WithTimeout(timeout),
	), nil
}

// model resolves an OpenRouter model id against the library's catalogue,
// declaring a capable custom model for an id it does not list.
func model(id string) llm.Model {
	for _, m := range llmopenrouter.Models {
		if m.APIModel == id {
			return m
		}
	}
	return llm.NewCustomModel(
		llm.WithModelID(id),
		llm.WithAPIModel(id),
		llm.WithName(id),
		llm.WithProvider(provider),
		llm.WithContextWindow(contextWindow),
		llm.WithStructuredOutput(true),
		llm.WithAttachments(true),
	)
}
