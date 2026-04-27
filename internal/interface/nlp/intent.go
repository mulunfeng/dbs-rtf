package nlp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dbs-rtf/agent/pkg/model"
)

type ParsedIntent struct {
	Plugin        string                 `json:"plugin"`
	Operation     string                 `json:"operation"`
	Instance      model.InstanceConfig   `json:"instance"`
	Params        map[string]string      `json:"params"`
	MissingFields []string               `json:"missing_fields"`
}

type IntentParser struct {
	template *IntentTemplate
}

func NewIntentParser() (*IntentParser, error) {
	template, err := LoadIntentTemplate()
	if err != nil {
		return nil, fmt.Errorf("load intent template: %w", err)
	}
	return &IntentParser{template: template}, nil
}

func (p *IntentParser) Parse(_ context.Context, userInput string) (*ParsedIntent, error) {
	// In production, this calls the LLM API (Claude/OpenAI etc.)
	// For now, return a placeholder error indicating LLM not configured
	return nil, fmt.Errorf("LLM provider not configured — set ANTHROPIC_API_KEY to enable NLP parsing")
}

func (p *IntentParser) ParseWithProvider(ctx context.Context, userInput string, llmCall func(ctx context.Context, prompt string) (string, error)) (*ParsedIntent, error) {
	prompt := BuildPrompt(p.template, userInput)

	response, err := llmCall(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	var intent ParsedIntent
	if err := json.Unmarshal([]byte(response), &intent); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w, response: %s", err, response)
	}

	if len(intent.MissingFields) > 0 {
		return &intent, fmt.Errorf("missing fields: %v", intent.MissingFields)
	}

	return &intent, nil
}

func (p *IntentParser) ToCommand(intent *ParsedIntent) model.Command {
	return model.Command{
		Plugin:     intent.Plugin,
		Operation:  intent.Operation,
		Instance:   intent.Instance,
		Params:     intent.Params,
	}
}
