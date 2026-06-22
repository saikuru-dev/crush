package agent

import (
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesProviderOptionsAddsInstructions(t *testing.T) {
	t.Parallel()

	systemPrompt := "You are a concise coding assistant."
	originalResponses := &openai.ResponsesProviderOptions{
		ReasoningSummary: stringPtr("auto"),
	}
	original := fantasy.ProviderOptions{
		openai.Name: originalResponses,
	}

	updated := openAIResponsesProviderOptions(
		Model{CatwalkCfg: catwalk.Model{ID: "gpt-5.4"}},
		systemPrompt,
		original,
	)

	raw, ok := updated[openai.Name]
	require.True(t, ok)
	responsesOpts, ok := raw.(*openai.ResponsesProviderOptions)
	require.True(t, ok)
	require.NotNil(t, responsesOpts.Instructions)
	assert.Equal(t, systemPrompt, *responsesOpts.Instructions)
	require.NotNil(t, responsesOpts.ReasoningSummary)
	assert.Equal(t, "auto", *responsesOpts.ReasoningSummary)

	require.NotNil(t, originalResponses.ReasoningSummary)
	assert.Nil(t, originalResponses.Instructions)
	assert.NotSame(t, originalResponses, responsesOpts)
}

func TestOpenAIResponsesProviderOptionsNoopForNonResponsesModels(t *testing.T) {
	t.Parallel()

	original := fantasy.ProviderOptions{
		openai.Name: &openai.ResponsesProviderOptions{},
	}

	updated := openAIResponsesProviderOptions(
		Model{CatwalkCfg: catwalk.Model{ID: "claude-3-5-sonnet"}},
		"You are a coding assistant.",
		original,
	)

	assert.Same(t, original[openai.Name], updated[openai.Name])
}

func TestStripLeadingSystemMessage(t *testing.T) {
	t.Parallel()

	messages := []fantasy.Message{
		fantasy.NewSystemMessage("system prompt"),
		fantasy.NewSystemMessage("prefix"),
		fantasy.NewUserMessage("hello"),
	}

	stripped := stripLeadingSystemMessage(messages)
	require.Len(t, stripped, 2)
	assert.Equal(t, fantasy.MessageRoleSystem, stripped[0].Role)
	assert.Equal(t, fantasy.MessageRoleUser, stripped[1].Role)
	assert.Equal(t, "prefix", textAt(t, stripped[0]))
	assert.Equal(t, "hello", textAt(t, stripped[1]))
}

func stringPtr(s string) *string {
	return &s
}

func textAt(t *testing.T, msg fantasy.Message) string {
	t.Helper()
	require.Len(t, msg.Content, 1)
	textPart, ok := fantasy.AsContentType[fantasy.TextPart](msg.Content[0])
	require.True(t, ok)
	return textPart.Text
}
