package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const protocolVersion = "2024-11-05"

type Option func(*Server)

type Server struct {
	handler http.Handler

	mu          sync.RWMutex
	accessToken string
	tools       map[string]toolDefinition
}

type toolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     func(context.Context, map[string]any) (any, error)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   *responseError `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

func NewServer(handler http.Handler, options ...Option) *Server {
	server := &Server{
		handler: handler,
	}
	for _, option := range options {
		option(server)
	}
	server.tools = server.registerTools()
	return server
}

func WithAccessToken(accessToken string) Option {
	return func(server *Server) {
		server.accessToken = strings.TrimSpace(accessToken)
	}
}

func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := readMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				_ = writer.Flush()
				return nil
			}
			return err
		}

		resp := s.handleRequest(ctx, req)
		if resp == nil {
			continue
		}
		if err := writeMessage(writer, resp); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, req request) *response {
	if req.JSONRPC == "" {
		req.JSONRPC = "2.0"
	}

	switch req.Method {
	case "initialize":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "alice",
					"version": "0.1.0",
				},
			},
		}
	case "ping":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}
	case "tools/list":
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.listTools(),
			},
		}
	case "tools/call":
		var params toolsCallParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return errorResponse(req.ID, -32602, "invalid tools/call params")
			}
		}
		tool, ok := s.tools[params.Name]
		if !ok {
			return errorResponse(req.ID, -32602, "unknown tool")
		}
		result, err := tool.Handler(ctx, params.Arguments)
		if err != nil {
			return &response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  toolResult(map[string]any{"error": err.Error()}, true),
			}
		}
		return &response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  toolResult(result, false),
		}
	default:
		if strings.HasPrefix(req.Method, "notifications/") || strings.HasPrefix(req.Method, "$/") {
			return nil
		}
		return errorResponse(req.ID, -32601, "method not found")
	}
}

func (s *Server) listTools() []map[string]any {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	sort.Strings(names)

	tools := make([]map[string]any, 0, len(names))
	for _, name := range names {
		tool := s.tools[name]
		tools = append(tools, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
		})
	}
	return tools
}

func toolResult(payload any, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": payloadText(payload),
			},
		},
		"structuredContent": payload,
		"isError":           isError,
	}
}

func payloadText(payload any) string {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprint(payload)
	}
	return string(data)
}

func errorResponse(id any, code int, message string) *response {
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
		},
	}
}

func readMessage(reader *bufio.Reader) (request, error) {
	headers := map[string]string{}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && len(headers) == 0 && line == "" {
				return request{}, io.EOF
			}
			return request{}, err
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			break
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return request{}, fmt.Errorf("invalid header line %q", trimmed)
		}
		headers[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}

	rawLength, ok := headers["content-length"]
	if !ok {
		return request{}, fmt.Errorf("missing Content-Length header")
	}

	length, err := strconv.Atoi(rawLength)
	if err != nil || length < 0 {
		return request{}, fmt.Errorf("invalid Content-Length %q", rawLength)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(reader, body); err != nil {
		return request{}, err
	}

	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return request{}, err
	}
	return req, nil
}

func writeMessage(writer *bufio.Writer, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := writer.WriteString(fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))); err != nil {
		return err
	}
	if _, err := writer.Write(body); err != nil {
		return err
	}
	return nil
}

func (s *Server) setAccessToken(accessToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.accessToken = strings.TrimSpace(accessToken)
}

func (s *Server) getAccessToken() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.accessToken
}
