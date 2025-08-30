package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// ---- Anthropic wire types ----

type ToolDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}
type ContentBlock struct {
	Type      string          `json:"type"` // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text,omitempty"`
	Name      string          `json:"name,omitempty"`
	ID        string          `json:"id,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Content   string          `json:"content,omitempty"`
}
type Msg struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}
type MessageReq struct {
	Model     string     `json:"model"`
	System    string     `json:"system,omitempty"`
	MaxTokens int        `json:"max_tokens"`
	Tools     []ToolDecl `json:"tools,omitempty"`
	Messages  []Msg      `json:"messages"`
}
type MessageResp struct {
	Content []ContentBlock `json:"content"`
}

// Build the system prompt with your actual defaults baked in (clear for the model)
func systemPrompt() string {
	p := os.Getenv("PROJECT")
	cf := os.Getenv("COMPOSE_FILE")
	ds := os.Getenv("DB_SERVICE")
	if p == "" {
		p = "unknown-project"
	}

	return fmt.Sprintf(
		`You are a cautious project-scoped Dev DB agent for %[1]q.
You manage docker compose for the database only.

Defaults:
- project = %[1]s
- compose_file = %[2]s
- db_service = %[3]s

Rules:
- Use composeUp/composeDown/waitHealthy/dbReset tools as needed.
- For destructive resets, require confirm_phrase = "RESET %[1]s".
- Keep responses short and actionable.`,
		p, cf, ds,
	)
}

func main() {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		fmt.Println("Set ANTHROPIC_API_KEY in .env")
		os.Exit(1)
	}
	model := os.Getenv("ANTHROPIC_MODEL")
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// Natural-language instruction comes from CLI args
	userInput := "Ramp up the DB and wait until it's ready."
	if len(os.Args) > 1 {
		userInput = strings.Join(os.Args[1:], " ")
	}

	msgs := []Msg{{Role: "user", Content: []ContentBlock{{Type: "text", Text: userInput}}}}

	for step := 0; step < 8; step++ {
		req := MessageReq{
			Model:     model,
			System:    systemPrompt(),
			MaxTokens: 700,
			Tools:     toolDecls(),
			Messages:  msgs,
		}
		resp, err := callAnthropic(key, req)
		if err != nil {
			fmt.Println("Anthropic error:", err)
			os.Exit(1)
		}
		// record assistant blocks
		msgs = append(msgs, Msg{Role: "assistant", Content: resp.Content})

		// handle tool calls (tool_use)
		used := false
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				used = true
				name := b.Name
				args := map[string]any{}
				_ = json.Unmarshal(b.Input, &args)
				fillDefaults(args) // pull from env if the model omitted something

				out, isErr, err := callTool(name, args)
				tres := ContentBlock{
					Type:      "tool_result",
					ToolUseID: b.ID,
					Content:   out,
					IsError:   isErr,
				}
				if err != nil {
					tres.Content = "Error: " + err.Error()
					tres.IsError = true
				}
				msgs = append(msgs, Msg{Role: "user", Content: []ContentBlock{tres}})
			}
		}
		if !used {
			// final text
			var sb strings.Builder
			for _, b := range resp.Content {
				if b.Type == "text" {
					sb.WriteString(b.Text)
					sb.WriteByte('\n')
				}
			}
			fmt.Print(strings.TrimSpace(sb.String()))
			return
		}
	}
	fmt.Println("Stopped after too many tool steps.")
}

func callAnthropic(key string, req MessageReq) (*MessageResp, error) {
	b, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(b))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", res.StatusCode, string(body))
	}
	var out MessageResp
	return &out, json.Unmarshal(body, &out)
}

// Inject env defaults if the model didn't supply them
func fillDefaults(m map[string]any) {
	if _, ok := m["project"]; !ok {
		m["project"] = os.Getenv("PROJECT")
	}
	if _, ok := m["compose_file"]; !ok {
		m["compose_file"] = os.Getenv("COMPOSE_FILE")
	}
	if _, ok := m["db_service"]; !ok {
		m["db_service"] = os.Getenv("DB_SERVICE")
	}
}
