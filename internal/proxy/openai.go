package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"miser/internal/tracker"
)

// OpenAI-compatible request/response types used by Cursor's "Override OpenAI Base URL".

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	MaxTokens   *int         `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
	TopP        *float64     `json:"top_p,omitempty"`
	Stream      bool         `json:"stream"`
	Stop        any          `json:"stop,omitempty"`
}

type oaiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicRequest struct {
	Model       string       `json:"model"`
	System      any          `json:"system,omitempty"`
	Messages    []oaiMessage `json:"messages"`
	MaxTokens   int          `json:"max_tokens"`
	Temperature *float64     `json:"temperature,omitempty"`
	TopP        *float64     `json:"top_p,omitempty"`
	Stream      bool         `json:"stream"`
	StopSeqs    any          `json:"stop_sequences,omitempty"`
}

type anthropicResponse struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Model   string `json:"model"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

type oaiResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []oaiChoice `json:"choices"`
	Usage   *oaiUsage   `json:"usage,omitempty"`
}

type oaiChoice struct {
	Index        int         `json:"index"`
	Message      *oaiMessage `json:"message,omitempty"`
	Delta        *oaiMessage `json:"delta,omitempty"`
	FinishReason *string     `json:"finish_reason"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":{"message":"failed to read request body"}}`, http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var oaiReq oaiRequest
	if err := json.Unmarshal(body, &oaiReq); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
		return
	}

	antReq := convertRequest(oaiReq)
	antBody, _ := json.Marshal(antReq)

	upURL := s.Target + "/v1/messages"
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upURL, bytes.NewReader(antBody))
	if err != nil {
		s.recordError(oaiReq.Model, start, err)
		http.Error(w, `{"error":{"message":"internal error"}}`, http.StatusInternalServerError)
		return
	}

	apiKey := r.Header.Get("Authorization")
	if strings.HasPrefix(apiKey, "Bearer ") {
		upReq.Header.Set("x-api-key", strings.TrimPrefix(apiKey, "Bearer "))
	}
	upReq.Header.Set("Content-Type", "application/json")
	upReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.client.Do(upReq)
	if err != nil {
		s.recordError(oaiReq.Model, start, err)
		http.Error(w, fmt.Sprintf(`{"error":{"message":"%s"}}`, err.Error()), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		s.recordError(oaiReq.Model, start, fmt.Errorf("upstream %d", resp.StatusCode))
		return
	}

	ct := resp.Header.Get("Content-Type")
	if oaiReq.Stream && strings.Contains(ct, "text/event-stream") {
		s.handleOAIStreaming(w, resp, oaiReq.Model, start)
	} else {
		s.handleOAINonStreaming(w, resp, oaiReq.Model, start)
	}
}

func (s *Server) handleOAINonStreaming(w http.ResponseWriter, resp *http.Response, model string, start time.Time) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.recordError(model, start, err)
		http.Error(w, `{"error":{"message":"failed to read upstream response"}}`, http.StatusBadGateway)
		return
	}

	var antResp anthropicResponse
	if err := json.Unmarshal(body, &antResp); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return
	}

	oaiResp := convertResponse(antResp)

	cost := tracker.CalculateCost(model,
		antResp.Usage.InputTokens, antResp.Usage.OutputTokens,
		antResp.Usage.CacheReadInputTokens, antResp.Usage.CacheCreationInputTokens)
	s.Tracker.Record(tracker.Request{
		Timestamp:    start,
		Model:        model,
		InputTokens:  antResp.Usage.InputTokens,
		OutputTokens: antResp.Usage.OutputTokens,
		CacheRead:    antResp.Usage.CacheReadInputTokens,
		CacheWrite:   antResp.Usage.CacheCreationInputTokens,
		Cost:         cost,
		Latency:      time.Since(start),
		StatusCode:   resp.StatusCode,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(oaiResp)
}

func (s *Server) handleOAIStreaming(w http.ResponseWriter, resp *http.Response, model string, start time.Time) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.handleOAINonStreaming(w, resp, model, start)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	var (
		inputTokens, outputTokens, cacheRead, cacheWrite int
		msgID                                             string
		sentRole                                          bool
	)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			continue
		}

		var event struct {
			Type    string `json:"type"`
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Index int `json:"index"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}

		switch event.Type {
		case "message_start":
			msgID = event.Message.ID
			inputTokens = event.Message.Usage.InputTokens
			cacheRead = event.Message.Usage.CacheReadInputTokens
			cacheWrite = event.Message.Usage.CacheCreationInputTokens

			if !sentRole {
				writeOAIChunk(w, flusher, msgID, model, &oaiMessage{Role: "assistant", Content: ""}, nil)
				sentRole = true
			}

		case "content_block_delta":
			if event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				writeOAIChunk(w, flusher, msgID, model, &oaiMessage{Content: event.Delta.Text}, nil)
			}

		case "message_delta":
			outputTokens = event.Usage.OutputTokens
			reason := mapStopReason(event.Delta.StopReason)
			writeOAIChunk(w, flusher, msgID, model, nil, &reason)

		case "message_stop":
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		}
	}

	cost := tracker.CalculateCost(model, inputTokens, outputTokens, cacheRead, cacheWrite)
	s.Tracker.Record(tracker.Request{
		Timestamp:    start,
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		CacheRead:    cacheRead,
		CacheWrite:   cacheWrite,
		Cost:         cost,
		Latency:      time.Since(start),
		StatusCode:   resp.StatusCode,
	})
}

func writeOAIChunk(w http.ResponseWriter, f http.Flusher, id, model string, delta *oaiMessage, finishReason *string) {
	chunk := oaiResponse{
		ID:      "chatcmpl-" + id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []oaiChoice{{
			Index:        0,
			Delta:        delta,
			FinishReason: finishReason,
		}},
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	f.Flush()
}

// --- converters ---

func convertRequest(oai oaiRequest) anthropicRequest {
	ant := anthropicRequest{
		Model:       oai.Model,
		Temperature: oai.Temperature,
		TopP:        oai.TopP,
		Stream:      oai.Stream,
		StopSeqs:    oai.Stop,
	}

	if oai.MaxTokens != nil {
		ant.MaxTokens = *oai.MaxTokens
	} else {
		ant.MaxTokens = 8192
	}

	for _, m := range oai.Messages {
		if m.Role == "system" {
			ant.System = m.Content
		} else {
			ant.Messages = append(ant.Messages, m)
		}
	}

	if len(ant.Messages) == 0 {
		ant.Messages = []oaiMessage{{Role: "user", Content: "Hello"}}
	}

	return ant
}

func convertResponse(ant anthropicResponse) oaiResponse {
	var text strings.Builder
	for _, c := range ant.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}

	reason := mapStopReason(ant.StopReason)

	return oaiResponse{
		ID:      "chatcmpl-" + ant.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   ant.Model,
		Choices: []oaiChoice{{
			Index:        0,
			Message:      &oaiMessage{Role: "assistant", Content: text.String()},
			FinishReason: &reason,
		}},
		Usage: &oaiUsage{
			PromptTokens:     ant.Usage.InputTokens,
			CompletionTokens: ant.Usage.OutputTokens,
			TotalTokens:      ant.Usage.InputTokens + ant.Usage.OutputTokens,
		},
	}
}

func mapStopReason(antReason string) string {
	switch antReason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	default:
		return "stop"
	}
}
