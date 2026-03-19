package edge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	connectorMaxAttempts = 3
	connectorBaseBackoff = 50 * time.Millisecond
	connectorMaxBackoff  = 500 * time.Millisecond
)

func doConnectorJSON[T any](ctx context.Context, client *http.Client, sourceName string, buildRequest func() (*http.Request, error)) (T, http.Header, error) {
	var zero T
	for attempt := 1; attempt <= connectorMaxAttempts; attempt++ {
		req, err := buildRequest()
		if err != nil {
			return zero, nil, fmt.Errorf("build %s request: %w", sourceName, err)
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < connectorMaxAttempts && shouldRetryConnectorError(err) {
				if err := sleepConnectorRetry(ctx, "", attempt); err != nil {
					return zero, nil, err
				}
				continue
			}
			return zero, nil, fmt.Errorf("perform %s request: %w", sourceName, err)
		}

		header := resp.Header.Clone()
		if retry := connectorRetryAfter(resp.StatusCode); retry {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if attempt < connectorMaxAttempts {
				if err := sleepConnectorRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil {
					return zero, nil, err
				}
				continue
			}
			return zero, header, fmt.Errorf("%s api returned status %d", sourceName, resp.StatusCode)
		}

		if resp.StatusCode >= http.StatusBadRequest {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			return zero, header, fmt.Errorf("%s api returned status %d", sourceName, resp.StatusCode)
		}

		var payload T
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if decodeErr != nil && !errors.Is(decodeErr, io.EOF) {
			return zero, header, fmt.Errorf("decode %s response: %w", sourceName, decodeErr)
		}
		return payload, header, nil
	}

	return zero, nil, fmt.Errorf("%s request exhausted retries", sourceName)
}

func connectorRetryAfter(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func shouldRetryConnectorError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func sleepConnectorRetry(ctx context.Context, retryAfter string, attempt int) error {
	delay := parseRetryAfter(retryAfter)
	if delay <= 0 {
		delay = connectorBackoff(attempt)
	}
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRetryAfter(value string) time.Duration {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(trimmed); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(trimmed); err == nil {
		delay := time.Until(retryAt)
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func connectorBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	delay := connectorBaseBackoff
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= connectorMaxBackoff {
			return connectorMaxBackoff
		}
	}
	if delay > connectorMaxBackoff {
		return connectorMaxBackoff
	}
	return delay
}
