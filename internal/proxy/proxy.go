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
	"strings"
	"time"

	"miser/internal/tracker"
)

type Server struct {
	Port    int
	Target  string
	Tracker *tracker.Tracker
	client  *http.Client
	logger  *log.Logger
}

func NewServer(port int, target string, timeout time.Duration, t *tracker.Tracker) *Server {
	return &Server{
		Port:    port,
		Target:  target,
		Tracker: t,
		logger:  log.New(io.Discard, "", 0),
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
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
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/chat/completions") {
		s.handleChatCompletions(w, r)
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/messages") {
		s.handleMessages(w, r)
		return
	}
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

	upstreamURL := s.Target + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		s.recordError(reqInfo.Model, start, err)
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	copyHeaders(upReq.Header, r.Header)

	resp, err := s.client.Do(upReq)
	if err != nil {
		s.recordError(reqInfo.Model, start, err)
		http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if reqInfo.Stream && strings.Contains(ct, "text/event-stream") {
		s.handleStreaming(w, resp, reqInfo.Model, start)
	} else {
		s.handleNonStreaming(w, resp, reqInfo.Model, start)
	}
}

func (s *Server) handleNonStreaming(w http.ResponseWriter, resp *http.Response, model string, start time.Time) {
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
			Timestamp:    start,
			Model:        model,
			InputTokens:  msg.Usage.InputTokens,
			OutputTokens: msg.Usage.OutputTokens,
			CacheRead:    msg.Usage.CacheReadInputTokens,
			CacheWrite:   msg.Usage.CacheCreationInputTokens,
			Cost:         cost,
			Latency:      time.Since(start),
			StatusCode:   resp.StatusCode,
		})
	}
}

func (s *Server) handleStreaming(w http.ResponseWriter, resp *http.Response, model string, start time.Time) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.handleNonStreaming(w, resp, model, start)
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

func (s *Server) recordError(model string, start time.Time, err error) {
	s.Tracker.Record(tracker.Request{
		Timestamp: start,
		Model:     model,
		Latency:   time.Since(start),
		Error:     err.Error(),
	})
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
