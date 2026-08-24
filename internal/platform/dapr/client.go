package dapr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"approvalflow/internal/platform/httpx"
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
	httpMethod := http.MethodPost
	var body []byte

	if payload == nil {
		httpMethod = http.MethodGet
	} else {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("marshal invoke payload: %w", err)
		}
	}

	status, raw, err := c.InvokeRaw(ctx, appID, method, httpMethod, body)
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
	if correlationID := httpx.CorrelationIDFromContext(ctx); correlationID != "" {
		req.Header.Set(httpx.CorrelationIDHeader, correlationID)
	}

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

type stateOptions struct {
	Concurrency string `json:"concurrency,omitempty"`
	Consistency string `json:"consistency,omitempty"`
}

type stateItem struct {
	Key     string        `json:"key"`
	Value   any           `json:"value"`
	ETag    string        `json:"etag,omitempty"`
	Options *stateOptions `json:"options,omitempty"`
}

type StateTransactionItem struct {
	Key        string
	Value      any
	CreateOnly bool
}

type stateTransactionOperation struct {
	Operation string    `json:"operation"`
	Request   stateItem `json:"request"`
}

type stateTransactionRequest struct {
	Operations []stateTransactionOperation `json:"operations"`
}

var ErrStateConflict = errors.New("state write conflict")

func (c *Client) PublishEvent(ctx context.Context, pubsubName string, topic string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	eventURL := fmt.Sprintf(
		"%s/v1.0/publish/%s/%s",
		c.baseURL,
		url.PathEscape(pubsubName),
		url.PathEscape(topic),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, eventURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create publish request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("publish event %s/%s: %w", pubsubName, topic, err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf("publish failed with status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}

	return nil
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

func (c *Client) SaveStateTransaction(
	ctx context.Context,
	store string,
	items []StateTransactionItem,
) error {
	if len(items) == 0 {
		return fmt.Errorf("save state transaction: no items")
	}

	operations := make([]stateTransactionOperation, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Key) == "" {
			return fmt.Errorf("save state transaction: empty key")
		}

		options := &stateOptions{Consistency: "strong"}
		if item.CreateOnly {
			options.Concurrency = "first-write"
		}
		operations = append(operations, stateTransactionOperation{
			Operation: "upsert",
			Request: stateItem{
				Key:     item.Key,
				Value:   item.Value,
				Options: options,
			},
		})
	}

	body, err := json.Marshal(stateTransactionRequest{Operations: operations})
	if err != nil {
		return fmt.Errorf("marshal state transaction: %w", err)
	}

	transactionURL := fmt.Sprintf(
		"%s/v1.0/state/%s/transaction",
		c.baseURL,
		url.PathEscape(store),
	)
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		transactionURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create state transaction request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("save state transaction: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf(
			"save state transaction failed with status %d: %s",
			res.StatusCode,
			strings.TrimSpace(string(raw)),
		)
	}

	return nil
}

func (c *Client) SaveStateWithETag(
	ctx context.Context,
	store string,
	key string,
	value any,
	etag string,
) error {
	if strings.TrimSpace(etag) == "" {
		return fmt.Errorf("save state with ETag: empty ETag")
	}

	items := []stateItem{
		{
			Key:   key,
			Value: value,
			ETag:  etag,
			Options: &stateOptions{
				Concurrency: "first-write",
			},
		},
	}

	body, err := json.Marshal(items)
	if err != nil {
		return fmt.Errorf("marshal conditional state item: %w", err)
	}

	stateURL := fmt.Sprintf(
		"%s/v1.0/state/%s",
		c.baseURL,
		url.PathEscape(store),
	)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		stateURL,
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create conditional save state request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("conditionally save state key %s: %w", key, err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w: key %s", ErrStateConflict, key)
	}

	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(res.Body)
		return fmt.Errorf(
			"conditional save state failed with status %d: %s",
			res.StatusCode,
			strings.TrimSpace(string(raw)),
		)
	}

	return nil
}

func (c *Client) GetSecret(ctx context.Context, store string, key string) (string, bool, error) {
	secretURL := fmt.Sprintf(
		"%s/v1.0/secrets/%s/%s",
		c.baseURL,
		url.PathEscape(store),
		url.PathEscape(key),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("create get secret request: %w", err)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("get secret %s/%s: %w", store, key, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return "", false, fmt.Errorf("read secret response: %w", err)
	}

	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusNotFound {
		return "", false, nil
	}

	if res.StatusCode >= 400 {
		return "", false, fmt.Errorf("get secret failed with status %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}

	var values map[string]string
	if err := json.Unmarshal(raw, &values); err != nil {
		return "", false, fmt.Errorf("decode secret %s/%s: %w", store, key, err)
	}

	value, ok := values[key]
	if !ok || value == "" {
		return "", false, nil
	}

	return value, true, nil
}

func (c *Client) GetState(
	ctx context.Context,
	store string,
	key string,
	out any,
) (bool, error) {
	return c.getState(ctx, store, key, "", out)
}

func (c *Client) GetStateStrong(
	ctx context.Context,
	store string,
	key string,
	out any,
) (bool, error) {
	return c.getState(ctx, store, key, "strong", out)
}

func (c *Client) getState(
	ctx context.Context,
	store string,
	key string,
	consistency string,
	out any,
) (bool, error) {
	stateURL := fmt.Sprintf(
		"%s/v1.0/state/%s/%s",
		c.baseURL,
		url.PathEscape(store),
		url.PathEscape(key),
	)
	if consistency != "" {
		stateURL += "?consistency=" + url.QueryEscape(consistency)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		stateURL,
		nil,
	)
	if err != nil {
		return false, fmt.Errorf(
			"create get state request: %w",
			err,
		)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf(
			"get state key %s: %w",
			key,
			err,
		)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNoContent ||
		res.StatusCode == http.StatusNotFound {
		return false, nil
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return false, fmt.Errorf(
			"read state response: %w",
			err,
		)
	}

	if res.StatusCode >= 400 {
		return false, fmt.Errorf(
			"get state failed with status %d: %s",
			res.StatusCode,
			strings.TrimSpace(string(raw)),
		)
	}

	if len(raw) == 0 || string(raw) == "null" {
		return false, nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf(
			"decode state key %s: %w",
			key,
			err,
		)
	}

	return true, nil
}

func (c *Client) GetStateWithETag(
	ctx context.Context,
	store string,
	key string,
	out any,
) (bool, string, error) {
	stateURL := fmt.Sprintf(
		"%s/v1.0/state/%s/%s",
		c.baseURL,
		url.PathEscape(store),
		url.PathEscape(key),
	)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		stateURL,
		nil,
	)
	if err != nil {
		return false, "", fmt.Errorf(
			"create get state request: %w",
			err,
		)
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return false, "", fmt.Errorf(
			"get state key %s: %w",
			key,
			err,
		)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNoContent ||
		res.StatusCode == http.StatusNotFound {
		return false, "", nil
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return false, "", fmt.Errorf(
			"read state response: %w",
			err,
		)
	}

	if res.StatusCode >= 400 {
		return false, "", fmt.Errorf(
			"get state failed with status %d: %s",
			res.StatusCode,
			strings.TrimSpace(string(raw)),
		)
	}

	if len(raw) == 0 || string(raw) == "null" {
		return false, "", nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return false, "", fmt.Errorf(
			"decode state key %s: %w",
			key,
			err,
		)
	}

	etag := strings.TrimSpace(res.Header.Get("ETag"))
	etag = strings.Trim(etag, "\"")

	if etag == "" {
		return false, "", fmt.Errorf(
			"get state key %s returned no ETag",
			key,
		)
	}

	return true, etag, nil
}

// InvokeRawPassthrough invokes another Dapr app and returns the downstream
// HTTP status/body even for business errors such as 400, 404, or 409.
// It should be used by API gateway proxy handlers that need to preserve
// downstream responses for clients.
func (c *Client) InvokeRawPassthrough(ctx context.Context, appID string, method string, httpMethod string, payload []byte) (int, []byte, error) {
	if httpMethod == "" {
		httpMethod = http.MethodPost
	}

	url := c.baseURL + "/v1.0/invoke/" + appID + "/method/" + method

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, url, body)
	if err != nil {
		return 0, nil, err
	}

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if correlationID := httpx.CorrelationIDFromContext(ctx); correlationID != "" {
		req.Header.Set(httpx.CorrelationIDHeader, correlationID)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}

	return resp.StatusCode, raw, nil
}
