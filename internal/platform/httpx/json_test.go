package httpx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type strictJSONFixture struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func TestDecodeJSONAcceptsValidJSON(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{"name":"Guy","age":30}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)
	if err != nil {
		t.Fatalf("DecodeJSON returned error: %v", err)
	}

	if payload.Name != "Guy" || payload.Age != 30 {
		t.Fatalf("unexpected decoded payload: %+v", payload)
	}
}

func TestDecodeJSONAcceptsJSONCharset(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{"name":"Guy","age":30}`),
	)
	req.Header.Set(
		"Content-Type",
		"application/json; charset=utf-8",
	)

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)
	if err != nil {
		t.Fatalf("DecodeJSON returned error: %v", err)
	}
}

func TestDecodeJSONAcceptsCloudEventsJSON(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/events/test",
		strings.NewReader(`{"name":"event","age":1}`),
	)
	req.Header.Set(
		"Content-Type",
		"application/cloudevents+json",
	)

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)
	if err != nil {
		t.Fatalf("DecodeJSON returned error: %v", err)
	}
}

func TestDecodeJSONRejectsMissingContentType(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{"name":"Guy"}`),
	)

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)

	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf(
			"expected ErrUnsupportedMediaType, got %v",
			err,
		)
	}
}

func TestDecodeJSONRejectsWrongContentType(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{"name":"Guy"}`),
	)
	req.Header.Set("Content-Type", "text/plain")

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)

	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf(
			"expected ErrUnsupportedMediaType, got %v",
			err,
		)
	}
}

func TestDecodeJSONRejectsUnknownField(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(
			`{"name":"Guy","unexpected":true}`,
		),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)

	if !errors.Is(err, ErrUnknownJSONField) {
		t.Fatalf(
			"expected ErrUnknownJSONField, got %v",
			err,
		)
	}
}

func TestDecodeJSONRejectsMalformedJSON(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{"name":`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)

	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf(
			"expected ErrMalformedJSON, got %v",
			err,
		)
	}
}

func TestDecodeJSONRejectsEmptyBody(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(""),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)

	if !errors.Is(err, ErrEmptyJSONBody) {
		t.Fatalf(
			"expected ErrEmptyJSONBody, got %v",
			err,
		)
	}
}

func TestDecodeJSONRejectsMultipleValues(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(
			`{"name":"first"} {"name":"second"}`,
		),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 1024)

	if !errors.Is(err, ErrMultipleJSONValues) {
		t.Fatalf(
			"expected ErrMultipleJSONValues, got %v",
			err,
		)
	}
}

func TestDecodeJSONRejectsOversizedBody(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(
			`{"name":"value exceeding the tiny limit"}`,
		),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	var payload strictJSONFixture
	err := DecodeJSON(rec, req, &payload, 10)

	if !errors.Is(err, ErrRequestBodyTooLarge) {
		t.Fatalf(
			"expected ErrRequestBodyTooLarge, got %v",
			err,
		)
	}
}

func TestWriteJSONDecodeErrorMapsUnsupportedMediaType(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		nil,
	)
	rec := httptest.NewRecorder()

	WriteJSONDecodeError(
		rec,
		req,
		ErrUnsupportedMediaType,
	)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf(
			"expected 415, got %d",
			rec.Code,
		)
	}
}

func TestWriteJSONDecodeErrorMapsOversizedBody(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		nil,
	)
	rec := httptest.NewRecorder()

	WriteJSONDecodeError(
		rec,
		req,
		ErrRequestBodyTooLarge,
	)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf(
			"expected 413, got %d",
			rec.Code,
		)
	}
}

func TestReadJSONBodyReturnsOriginalValidBody(t *testing.T) {
	body := `{"name":"Guy","age":30}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	got, err := ReadJSONBody(rec, req, 1024)
	if err != nil {
		t.Fatalf("ReadJSONBody returned error: %v", err)
	}

	if string(got) != body {
		t.Fatalf("expected original body %q, got %q", body, string(got))
	}
}

func TestReadJSONBodyRejectsMultipleValues(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{"name":"one"} {"name":"two"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	_, err := ReadJSONBody(rec, req, 1024)

	if !errors.Is(err, ErrMultipleJSONValues) {
		t.Fatalf(
			"expected ErrMultipleJSONValues, got %v",
			err,
		)
	}
}

func TestReadJSONBodyRejectsOversizedBody(t *testing.T) {
	req := httptest.NewRequest(
		http.MethodPost,
		"/test",
		strings.NewReader(`{"name":"body larger than limit"}`),
	)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	_, err := ReadJSONBody(rec, req, 8)

	if !errors.Is(err, ErrRequestBodyTooLarge) {
		t.Fatalf(
			"expected ErrRequestBodyTooLarge, got %v",
			err,
		)
	}
}
