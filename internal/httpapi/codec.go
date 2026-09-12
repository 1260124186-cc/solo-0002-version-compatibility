package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	"solo-0002-version-compatibility/internal/domain"
)

const maxBodyBytes = 64 << 10

func decode(w http.ResponseWriter, r *http.Request, target any) error {
	return decodeLimit(w, r, target, maxBodyBytes)
}

func decodeLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return &domain.Fault{Code: "unsupported_media", Detail: "Content-Type must be application/json"}
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			return &domain.Fault{Code: "body_too_large", Detail: fmt.Sprintf("JSON body exceeds %d bytes", limit)}
		}
		return domain.Invalid("cannot read JSON body")
	}
	trimmed := bytes.TrimSpace(data)
	if !utf8.Valid(data) {
		return domain.Invalid("JSON body must be valid UTF-8")
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return domain.Invalid("JSON body must be an object")
	}
	if err := uniqueKeys(data); err != nil {
		return domain.Invalid("invalid JSON: %s", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return domain.Invalid("invalid JSON: %s", err)
	}
	return nil
}

func respond(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"internal_error","detail":"cannot encode response"}}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func fail(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	fault := &domain.Fault{Code: "internal_error", Detail: "request could not be completed"}
	var known *domain.Fault
	if errors.As(err, &known) {
		fault = known
		switch known.Code {
		case "invalid_input":
			status = http.StatusBadRequest
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		case "no_solution", "limit_exceeded":
			status = http.StatusUnprocessableEntity
		case "unsupported_media":
			status = http.StatusUnsupportedMediaType
		case "body_too_large":
			status = http.StatusRequestEntityTooLarge
		}
	} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		status = http.StatusRequestTimeout
		fault = &domain.Fault{Code: "request_timeout", Detail: "request deadline reached"}
	}
	respond(w, status, map[string]any{"error": fault})
}

func uniqueKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("unexpected trailing value")
	}
	return nil
}

func scanValue(decoder *json.Decoder, depth int) error {
	if depth > 20 {
		return fmt.Errorf("JSON nesting exceeds limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("object key must be a string")
			}
			if seen[name] {
				return fmt.Errorf("duplicate object key %q", name)
			}
			seen[name] = true
			if err := scanValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := scanValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected delimiter")
	}
	_, err = decoder.Token()
	return err
}
