package nlp

import (
	_ "embed"
	"gopkg.in/yaml.v3"
)

//go:embed templates/intent.yaml
var intentTemplate []byte

type IntentTemplate struct {
	SystemPrompt string    `yaml:"system_prompt"`
	Examples     []Example `yaml:"examples"`
}

type Example struct {
	User   string `yaml:"user"`
	Output string `yaml:"output"`
}

func LoadIntentTemplate() (*IntentTemplate, error) {
	var t IntentTemplate
	if err := yaml.Unmarshal(intentTemplate, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func BuildPrompt(template *IntentTemplate, userInput string) string {
	prompt := template.SystemPrompt + "\n\n"
	prompt += "User input: " + userInput + "\n"
	prompt += "Output your response in JSON format."
	return prompt
}
