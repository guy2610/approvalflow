package dapr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewFromEnv() *Client {
	port := os.Getenv("DAPR_HTTP_PORT")
	if port == "" {
		port = "3500"
	}

	return &Client{
		baseURL: "http://127.0.0.1:" + port,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *Client) InvokeJSON(ctx context.Context, appID string, method string, payload any, out any) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal invoke payload: %w", err)
	}

	status, raw, err := c.InvokeRaw(ctx, appID, method, http.MethodPost, body)
	if err != nil {
		return status, err
	}

	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return status, fmt.Errorf("decode invoke response: %w", err)
		}
	}

	return status, nil
}

func (c *Client) InvokeRaw(ctx context.Context, appID string, method string, httpMethod string, body []byte) (int, []byte, error) {
	method = strings.TrimPrefix(method, "/")

	invokeURL := fmt.Sprintf(
		"%s/v1.0/invoke/%s/method/%s",
		c.baseURL,
		url.PathEscape(appID),
		method,
	)

	req, err := http.NewRequestWithContext(ctx, httpMethod, invokeURL, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("create invoke request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("invoke %s/%s: %w", appID, method, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, nil, fmt.Errorf("read invoke response: %w", err)
	}

	if res.StatusCode >= 400 {
		return res.StatusCode, raw, fmt.Errorf("invoke failed with status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}

	return res.StatusCode, raw, nil
}

type stateItem struct {
	Key   string `json:"key"`
	Value any   `json:"value"`
}

func (c *Client) SaveState(ctx context.Context, store string, key string, value any) error {
	items := []stateItem{
		{
			Key:   key,
			Value: value,
		},
	}

	body, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("marshal state item: %w", err)
	}

	stateURL := fmt.Sprintf("%s/v1.0/state/%s", c.baseURL, url.PathEscape(store))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stateURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create save state request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("save state key %s: %w", key, err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("save state failed with status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}

	return nil
}

func (c *Client) GetState(ctx context.Context, store string, key string, out any) (bool, error) {
	stateURL := fmt.Sprintf(
		"%s/v1.0/state/%s/%s",
		c.baseURL,
		url.PathEscape(store),
		url.PathEscape(key),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stateURL, nil)
	if err != nil {
		return false, fmt.Errorf("create get state request: %w", err)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("get state key %s: %w", key, err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusNotFound {
		return false, nil
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return false, fmt.Errorf("read state response: %w", err)
	}

	if res.StatusCode >= 400 {
		return false, fmt.Errorf("get state failed with status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}

	if len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("decode state key %s: %w", key, err)
	}

	return true, nil
}
