package relay

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

func TestApplyMiniMaxCompatibility_PreserveToolsAndFlattenToolHistory(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	includeUsage := true
	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		StreamOptions: &dto.StreamOptions{
			IncludeUsage: includeUsage,
		},
		FunctionCall: []byte(`{"name":"legacy_func"}`),
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name: "search",
				},
			},
		},
		ToolChoice: map[string]any{
			"type": "auto",
		},
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "hello",
					},
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "https://example.com/a.png",
						},
					},
				},
			},
			{
				Role:             "assistant",
				Content:          nil,
				ReasoningContent: "internal thinking",
				ToolCalls:        []byte(`[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{}"}}]`),
			},
			{
				Role: "tool",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "tool result",
					},
				},
				ToolCallId: "call_1",
			},
		},
	}

	applyMiniMaxCompatibility(c, request)

	if request.StreamOptions != nil {
		t.Fatalf("expected stream_options to be removed")
	}
	if len(request.FunctionCall) != 0 {
		t.Fatalf("expected function_call to be removed")
	}
	if len(request.Tools) != 1 {
		t.Fatalf("expected tools to be preserved, got %d", len(request.Tools))
	}
	if request.ToolChoice == nil {
		t.Fatalf("expected tool_choice to be preserved")
	}
	if len(request.Messages) != 1 {
		t.Fatalf("expected tool history to be flattened, got %d messages", len(request.Messages))
	}

	if got := request.Messages[0].StringContent(); got != "hello" {
		t.Fatalf("expected user content to be normalized to text, got %q", got)
	}
	if request.Messages[0].Role != "user" {
		t.Fatalf("expected remaining message to be user, got %s", request.Messages[0].Role)
	}
}

func TestApplyMiniMaxCompatibility_ImageOnlyContentBecomesEmptyString(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []any{
					map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "https://example.com/image.png",
						},
					},
				},
			},
		},
	}

	applyMiniMaxCompatibility(c, request)

	if len(request.Messages) != 1 {
		t.Fatalf("expected message count unchanged, got %d", len(request.Messages))
	}
	if !request.Messages[0].IsStringContent() {
		t.Fatalf("expected image-only content to be normalized to string")
	}
	if got := request.Messages[0].StringContent(); got != "" {
		t.Fatalf("expected empty string after removing unsupported non-text content, got %q", got)
	}
}

func TestApplyMiniMaxCompatibility_MediaContentSliceToString(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		Messages: []dto.Message{
			{
				Role: "user",
				Content: []dto.MediaContent{
					{
						Type: dto.ContentTypeText,
						Text: "line1",
					},
					{
						Type: dto.ContentTypeText,
						Text: "line2",
					},
					{
						Type: dto.ContentTypeImageURL,
						ImageUrl: &dto.MessageImageUrl{
							Url: "https://example.com/ignored.png",
						},
					},
				},
			},
		},
	}

	applyMiniMaxCompatibility(c, request)

	if !request.Messages[0].IsStringContent() {
		t.Fatalf("expected media content slice to be normalized into string")
	}
	if got := request.Messages[0].StringContent(); got != "line1\nline2" {
		t.Fatalf("unexpected normalized content, got %q", got)
	}
}

func TestApplyMiniMaxCompatibility_RemoveUnknownToolCallsAndToolMessages(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		Tools: []dto.ToolCallRequest{
			{
				Type: "function",
				Function: dto.FunctionRequest{
					Name: "computer_use_builtin__open_app",
				},
			},
		},
		Messages: []dto.Message{
			{
				Role:    "assistant",
				Content: "calling tools",
				ToolCalls: []byte(`[
{"id":"call_old","type":"function","function":{"name":"computer_use_builtin__command","arguments":"{}"}},
{"id":"call_ok","type":"function","function":{"name":"computer_use_builtin__open_app","arguments":"{}"}}
]`),
			},
			{
				Role:       "tool",
				ToolCallId: "call_old",
				Content:    "old tool result",
			},
			{
				Role:       "tool",
				ToolCallId: "call_ok",
				Content:    "ok tool result",
			},
		},
	}

	applyMiniMaxCompatibility(c, request)

	if len(request.Messages) != 1 {
		t.Fatalf("expected tool transcript to be flattened with assistant text retained, got %d messages", len(request.Messages))
	}
	if request.Messages[0].Role != "assistant" {
		t.Fatalf("expected remaining message role assistant, got %s", request.Messages[0].Role)
	}
	if request.Messages[0].StringContent() != "calling tools" {
		t.Fatalf("expected remaining assistant text, got %q", request.Messages[0].StringContent())
	}
	if len(request.Messages[0].ToolCalls) != 0 {
		t.Fatalf("expected assistant tool_calls removed from history")
	}
}

func TestApplyMiniMaxCompatibility_MergeSystemAndConsecutiveRoles(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)

	request := &dto.GeneralOpenAIRequest{
		Model: "MiniMax-M2.7",
		Messages: []dto.Message{
			{Role: "user", Content: "u1"},
			{Role: "system", Content: "s1"},
			{Role: "system", Content: "s2"},
			{Role: "user", Content: "u2"},
			{Role: "assistant", Content: "a1"},
			{Role: "assistant", Content: "a2"},
		},
	}

	applyMiniMaxCompatibility(c, request)

	if len(request.Messages) != 3 {
		t.Fatalf("expected merged message count 3, got %d", len(request.Messages))
	}
	if request.Messages[0].Role != "system" {
		t.Fatalf("expected first message role system, got %s", request.Messages[0].Role)
	}
	if got := request.Messages[0].StringContent(); got != "s1\n\ns2" {
		t.Fatalf("expected merged system content, got %q", got)
	}
	if request.Messages[1].Role != "user" {
		t.Fatalf("expected second message role user, got %s", request.Messages[1].Role)
	}
	if got := request.Messages[1].StringContent(); got != "u1\n\nu2" {
		t.Fatalf("expected merged user content, got %q", got)
	}
	if request.Messages[2].Role != "assistant" {
		t.Fatalf("expected third message role assistant, got %s", request.Messages[2].Role)
	}
	if got := request.Messages[2].StringContent(); got != "a1\n\na2" {
		t.Fatalf("expected merged assistant content, got %q", got)
	}
}

func TestShouldRetryMiniMaxDoRequestError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "connection reset by peer is retryable",
			err:  errors.New(`Post "https://api.minimax.io/v1/text/chatcompletion_v2": read tcp 127.0.0.1:1->1.1.1.1:443: read: connection reset by peer`),
			want: true,
		},
		{
			name: "context deadline exceeded is retryable",
			err:  errors.New("context deadline exceeded"),
			want: true,
		},
		{
			name: "business error is not retryable",
			err:  errors.New("invalid params, invalid chat setting"),
			want: false,
		},
		{
			name: "nil error is not retryable",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetryMiniMaxDoRequestError(tt.err)
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
