package web

// Governing: SPEC-0024 REQ-1 (Endpoint Registration), REQ-7 (Error Response Format), ADR-0020
// OpenAI-compatible Go structs for the /v1/chat/completions endpoint.

// ChatRequest is the OpenAI-compatible chat completion request body.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// ChatMessage represents a single message in the OpenAI messages array.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionChunk is an SSE chunk in the OpenAI streaming format.
type ChatCompletionChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
}

// ChatCompletion is the non-streaming OpenAI chat completion response.
type ChatCompletion struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Model   string             `json:"model"`
	Choices []CompletionChoice `json:"choices"`
	Usage   ChatUsage          `json:"usage"`
}

// CompletionChoice is a choice in a non-streaming completion response.
type CompletionChoice struct {
	Index        int                `json:"index"`
	Message      ChatMessage        `json:"message"`
	FinishReason string             `json:"finish_reason"`
}

// Choice is a choice in a streaming completion chunk.
type Choice struct {
	Index        int    `json:"index"`
	Delta        Delta  `json:"delta"`
	FinishReason string `json:"finish_reason,omitempty"`
}

// Delta represents incremental content in a streaming chunk.
type Delta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a tool invocation in the OpenAI function calling format.
type ToolCall struct {
	Index    int          `json:"index"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction holds the tool name and JSON-encoded arguments string.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatUsage holds token usage counts (zeroed for Claude Ops).
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIError is the top-level error response wrapper.
type OpenAIError struct {
	Error OpenAIErrorDetail `json:"error"`
}

// OpenAIErrorDetail holds the error message, type, and code.
type OpenAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}
