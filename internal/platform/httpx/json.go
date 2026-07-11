package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

const DefaultMaxJSONBodyBytes int64 = 1 << 20

var (
	ErrUnsupportedMediaType = errors.New("unsupported media type")
	ErrRequestBodyTooLarge  = errors.New("request body too large")
	ErrMalformedJSON        = errors.New("malformed JSON")
	ErrUnknownJSONField     = errors.New("unknown JSON field")
	ErrMultipleJSONValues   = errors.New("multiple JSON values")
	ErrEmptyJSONBody        = errors.New("empty JSON body")
)

func ReadJSONBody(
	w http.ResponseWriter,
	r *http.Request,
	maxBytes int64,
) ([]byte, error) {
	if err := validateJSONContentType(r); err != nil {
		return nil, err
	}

	if maxBytes <= 0 {
		maxBytes = DefaultMaxJSONBodyBytes
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, classifyJSONDecodeError(err)
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, ErrEmptyJSONBody
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, classifyJSONDecodeError(err)
	}

	var trailing any
	err = decoder.Decode(&trailing)

	switch {
	case errors.Is(err, io.EOF):
		return raw, nil
	case err == nil:
		return nil, ErrMultipleJSONValues
	default:
		return nil, classifyJSONDecodeError(err)
	}
}

func DecodeJSON(
	w http.ResponseWriter,
	r *http.Request,
	out any,
	maxBytes int64,
) error {
	if err := validateJSONContentType(r); err != nil {
		return err
	}

	if maxBytes <= 0 {
		maxBytes = DefaultMaxJSONBodyBytes
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(out); err != nil {
		return classifyJSONDecodeError(err)
	}

	var trailing any
	err := decoder.Decode(&trailing)

	switch {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return ErrMultipleJSONValues
	default:
		return classifyJSONDecodeError(err)
	}
}

func validateJSONContentType(r *http.Request) error {
	contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
	if contentType == "" {
		return ErrUnsupportedMediaType
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ErrUnsupportedMediaType
	}

	if mediaType != "application/json" &&
		mediaType != "application/cloudevents+json" {
		return ErrUnsupportedMediaType
	}

	return nil
}

func classifyJSONDecodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return fmt.Errorf(
			"%w: limit is %d bytes",
			ErrRequestBodyTooLarge,
			maxBytesError.Limit,
		)
	}

	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return fmt.Errorf(
			"%w at byte %d",
			ErrMalformedJSON,
			syntaxError.Offset,
		)
	}

	var typeError *json.UnmarshalTypeError
	if errors.As(err, &typeError) {
		return fmt.Errorf(
			"%w: invalid value for field %q",
			ErrMalformedJSON,
			typeError.Field,
		)
	}

	if errors.Is(err, io.EOF) {
		return ErrEmptyJSONBody
	}

	const unknownFieldPrefix = "json: unknown field "
	if strings.HasPrefix(err.Error(), unknownFieldPrefix) {
		field := strings.TrimPrefix(err.Error(), unknownFieldPrefix)

		return fmt.Errorf(
			"%w: %s",
			ErrUnknownJSONField,
			field,
		)
	}

	return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
}

func WriteJSONDecodeError(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	switch {
	case errors.Is(err, ErrUnsupportedMediaType):
		WriteError(
			w,
			r,
			http.StatusUnsupportedMediaType,
			"Content-Type must be application/json",
		)

	case errors.Is(err, ErrRequestBodyTooLarge):
		WriteError(
			w,
			r,
			http.StatusRequestEntityTooLarge,
			"request body exceeds the allowed size",
		)

	case errors.Is(err, ErrUnknownJSONField):
		WriteError(
			w,
			r,
			http.StatusBadRequest,
			err.Error(),
		)

	case errors.Is(err, ErrMultipleJSONValues):
		WriteError(
			w,
			r,
			http.StatusBadRequest,
			"request body must contain exactly one JSON value",
		)

	case errors.Is(err, ErrEmptyJSONBody):
		WriteError(
			w,
			r,
			http.StatusBadRequest,
			"request body must not be empty",
		)

	default:
		WriteError(
			w,
			r,
			http.StatusBadRequest,
			"request body contains invalid JSON",
		)
	}
}
