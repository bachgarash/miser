package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"miser/internal/compress"
	"miser/internal/tracker"
)

type Server struct {
	Port           int
	Target         string
	Tracker        *tracker.Tracker
	CompressConfig compress.Config
	client         *http.Client
	logger         *log.Logger
}

func NewServer(port int, target string, timeout time.Duration, t *tracker.Tracker, cc compress.Config) *Server {
	return &Server{
		Port:           port,
		Target:         target,
		Tracker:        t,
		CompressConfig: cc,
		logger:         log.New(os.Stderr, "[proxy] ", log.LstdFlags),
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (s *Server) compressionEnabled() bool {
	return s.CompressConfig.Whitespace || s.CompressConfig.StackTruncation || s.CompressConfig.Deduplication
}

// Start runs the HTTP server until ctx is cancelled, then shuts down gracefully.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.Port),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx)
	}()

	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	s.logger.Printf("[DEBUG] %s %s", r.Method, r.URL.Path)
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/chat/completions") {
		s.handleChatCompletions(w, r)
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/messages") {
		s.handleMessages(w, r)
		return
	}
	s.logger.Printf("[DEBUG] passthrough: %s %s", r.Method, r.URL.Path)
	s.passthrough(w, r)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	var reqInfo struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	json.Unmarshal(body, &reqInfo)
	s.logger.Printf("[DEBUG] handleMessages model=%q stream=%v bodyLen=%d", reqInfo.Model, reqInfo.Stream, len(body))

	var compStats compress.Stats
	if s.compressionEnabled() {
		body, compStats = s.compressAnthropicBody(body)
	}

	upstreamURL := s.Target + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		s.recordError(reqInfo.Model, start, err, compStats)
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	copyHeaders(upReq.Header, r.Header)

	resp, err := s.client.Do(upReq)
	if err != nil {
		s.recordError(reqInfo.Model, start, err, compStats)
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if reqInfo.Stream && strings.Contains(ct, "text/event-stream") {
		s.handleStreaming(w, resp, reqInfo.Model, start, compStats)
	} else {
		s.handleNonStreaming(w, resp, reqInfo.Model, start, compStats)
	}
}

func (s *Server) handleNonStreaming(w http.ResponseWriter, resp *http.Response, model string, start time.Time, cs compress.Stats) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.recordError(model, start, err)
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
		return
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	w.Write(body)

	var msg struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(body, &msg) == nil {
		cost := tracker.CalculateCost(model,
			msg.Usage.InputTokens, msg.Usage.OutputTokens,
			msg.Usage.CacheReadInputTokens, msg.Usage.CacheCreationInputTokens)
		s.Tracker.Record(tracker.Request{
			Timestamp:      start,
			Model:          model,
			InputTokens:    msg.Usage.InputTokens,
			OutputTokens:   msg.Usage.OutputTokens,
			CacheRead:      msg.Usage.CacheReadInputTokens,
			CacheWrite:     msg.Usage.CacheCreationInputTokens,
			Cost:           cost,
			Latency:        time.Since(start),
			StatusCode:     resp.StatusCode,
			OriginalSize:   cs.OriginalBytes,
			CompressedSize: cs.CompressedBytes,
		})
	}
}

func (s *Server) handleStreaming(w http.ResponseWriter, resp *http.Response, model string, start time.Time, cs compress.Stats) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.handleNonStreaming(w, resp, model, start, cs)
		return
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	var inputTokens, outputTokens, cacheRead, cacheWrite int

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(w, "%s\n", line)
		flusher.Flush()

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
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			continue
		}
		switch event.Type {
		case "message_start":
			inputTokens = event.Message.Usage.InputTokens
			cacheRead = event.Message.Usage.CacheReadInputTokens
			cacheWrite = event.Message.Usage.CacheCreationInputTokens
		case "message_delta":
			outputTokens = event.Usage.OutputTokens
		}
	}

	s.logger.Printf("[DEBUG] streaming done model=%q input=%d output=%d cacheR=%d cacheW=%d",
		model, inputTokens, outputTokens, cacheRead, cacheWrite)
	if err := scanner.Err(); err != nil {
		s.logger.Printf("[DEBUG] scanner error: %v", err)
	}
	cost := tracker.CalculateCost(model, inputTokens, outputTokens, cacheRead, cacheWrite)
	s.Tracker.Record(tracker.Request{
		Timestamp:      start,
		Model:          model,
		InputTokens:    inputTokens,
		OutputTokens:   outputTokens,
		CacheRead:      cacheRead,
		CacheWrite:     cacheWrite,
		Cost:           cost,
		Latency:        time.Since(start),
		StatusCode:     resp.StatusCode,
		OriginalSize:   cs.OriginalBytes,
		CompressedSize: cs.CompressedBytes,
	})
}

func (s *Server) passthrough(w http.ResponseWriter, r *http.Request) {
	upstreamURL := s.Target + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	copyHeaders(upReq.Header, r.Header)

	resp, err := s.client.Do(upReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Server) recordError(model string, start time.Time, err error, cs ...compress.Stats) {
	r := tracker.Request{
		Timestamp: start,
		Model:     model,
		Latency:   time.Since(start),
		Error:     err.Error(),
	}
	if len(cs) > 0 {
		r.OriginalSize = cs[0].OriginalBytes
		r.CompressedSize = cs[0].CompressedBytes
	}
	s.Tracker.Record(r)
}

// compressAnthropicBody extracts text from system and messages fields,
// runs the compression pipeline, and writes the text back. All unknown
// fields (tool_use, metadata, etc.) are preserved. On any error the
// original body is returned unmodified (fail-open).
func (s *Server) compressAnthropicBody(body []byte) ([]byte, compress.Stats) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, compress.Stats{}
	}

	var msgs []compress.Message
	idx := 0

	// Extract system text.
	if sysRaw, ok := raw["system"]; ok {
		var sysStr string
		if json.Unmarshal(sysRaw, &sysStr) == nil {
			msgs = append(msgs, compress.Message{Index: idx, Role: "system", Content: sysStr})
			idx++
		} else {
			// system can be an array of content blocks
			var blocks []map[string]interface{}
			if json.Unmarshal(sysRaw, &blocks) == nil {
				for _, block := range blocks {
					if t, _ := block["type"].(string); t == "text" {
						if text, _ := block["text"].(string); text != "" {
							msgs = append(msgs, compress.Message{Index: idx, Role: "system", Content: text})
							idx++
						}
					}
				}
			}
		}
	}

	// Extract messages text.
	var apiMsgs []json.RawMessage
	if msgRaw, ok := raw["messages"]; ok {
		json.Unmarshal(msgRaw, &apiMsgs)
	}

	type parsedMsg struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}

	for _, rawMsg := range apiMsgs {
		var pm parsedMsg
		if json.Unmarshal(rawMsg, &pm) != nil {
			idx++
			continue
		}

		// Content can be a string or array of content blocks.
		var contentStr string
		if json.Unmarshal(pm.Content, &contentStr) == nil {
			msgs = append(msgs, compress.Message{Index: idx, Role: pm.Role, Content: contentStr})
		} else {
			var blocks []map[string]interface{}
			if json.Unmarshal(pm.Content, &blocks) == nil {
				for _, block := range blocks {
					if t, _ := block["type"].(string); t == "text" {
						if text, _ := block["text"].(string); text != "" {
							msgs = append(msgs, compress.Message{Index: idx, Role: pm.Role, Content: text})
						}
					}
				}
			}
		}
		idx++
	}

	if len(msgs) == 0 {
		return body, compress.Stats{}
	}

	compressed, stats := compress.Compress(s.CompressConfig, msgs)
	if stats.OriginalBytes == stats.CompressedBytes {
		return body, stats
	}

	// Write compressed text back into the body.
	ci := 0 // index into compressed slice

	// Write back system.
	if sysRaw, ok := raw["system"]; ok {
		var sysStr string
		if json.Unmarshal(sysRaw, &sysStr) == nil && ci < len(compressed) {
			newSys, _ := json.Marshal(compressed[ci].Content)
			raw["system"] = newSys
			ci++
		} else {
			var blocks []map[string]interface{}
			if json.Unmarshal(sysRaw, &blocks) == nil {
				for i, block := range blocks {
					if t, _ := block["type"].(string); t == "text" {
						if _, ok := block["text"].(string); ok && ci < len(compressed) {
							blocks[i]["text"] = compressed[ci].Content
							ci++
						}
					}
				}
				newSys, _ := json.Marshal(blocks)
				raw["system"] = newSys
			}
		}
	}

	// Write back messages.
	for mi, rawMsg := range apiMsgs {
		var pm parsedMsg
		if json.Unmarshal(rawMsg, &pm) != nil {
			continue
		}

		var msgMap map[string]json.RawMessage
		json.Unmarshal(rawMsg, &msgMap)

		var contentStr string
		if json.Unmarshal(pm.Content, &contentStr) == nil {
			if ci < len(compressed) {
				newContent, _ := json.Marshal(compressed[ci].Content)
				msgMap["content"] = newContent
				ci++
			}
		} else {
			var blocks []map[string]interface{}
			if json.Unmarshal(pm.Content, &blocks) == nil {
				for bi, block := range blocks {
					if t, _ := block["type"].(string); t == "text" {
						if _, ok := block["text"].(string); ok && ci < len(compressed) {
							blocks[bi]["text"] = compressed[ci].Content
							ci++
						}
					}
				}
				newContent, _ := json.Marshal(blocks)
				msgMap["content"] = newContent
			}
		}

		updatedMsg, _ := json.Marshal(msgMap)
		apiMsgs[mi] = updatedMsg
	}

	newMsgs, _ := json.Marshal(apiMsgs)
	raw["messages"] = newMsgs

	newBody, err := json.Marshal(raw)
	if err != nil {
		return body, compress.Stats{} // fail-open
	}
	return newBody, stats
}

var hopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailer":             true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
	"Host":                true,
}

func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		if hopHeaders[k] {
			continue
		}
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
