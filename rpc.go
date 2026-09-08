// Package-level RPC logic for the plugin. This file intentionally has no cgo
// dependency so the host-facing JSON contract can be type-checked and unit
// tested on any platform; main.go only contains the thin C-ABI export glue.
package main

import (
	"encoding/json"
	"fmt"

	"cliproxy-thinking-mask/internal/mask"
)

var masker = mask.New(mask.DefaultConfig())

// abiVersion is the native C ABI version the plugin implements (1).
const abiVersion uint32 = 1

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

type registration struct {
	SchemaVersion uint32           `json:"schema_version"`
	Metadata      registrationMeta `json:"metadata"`
	Capabilities  registrationCaps `json:"capabilities"`
}

type registrationMeta struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository"`
	Logo             string        `json:"Logo"`
	ConfigFields     []configField `json:"ConfigFields"`
}

type configField struct {
	Name        string `json:"Name"`
	Type        string `json:"Type"`
	Description string `json:"Description"`
}

type registrationCaps struct {
	ResponseStreamInterceptor bool `json:"response_stream_interceptor"`
}

type streamChunkRequest struct {
	RequestID  string `json:"RequestID"`
	ChunkIndex int    `json:"ChunkIndex"`
	Body       []byte `json:"Body"`
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		applyLifecycleConfig(request)
		return marshalResult(registrationResult())
	case "plugin.quiesce", "plugin.shutdown":
		return marshalResult(struct{}{})
	case "response.intercept_stream_chunk":
		return handleInterceptStreamChunk(request)
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

func registrationResult() registration {
	return registration{
		SchemaVersion: abiVersion,
		Metadata: registrationMeta{
			Name:             "cliproxy-thinking-mask",
			Version:          "0.1.0",
			Author:           "cliproxy-thinking-mask",
			GitHubRepository: "",
			Logo:             "",
			ConfigFields: []configField{
				{
					Name:        "thinking-text",
					Type:        "string",
					Description: "Text attached as delta.reasoning_content to the first content frame.",
				},
			},
		},
		Capabilities: registrationCaps{
			ResponseStreamInterceptor: true,
		},
	}
}

// applyLifecycleConfig updates the plugin from the config_yaml the host sends
// with plugin.register/plugin.reconfigure. Parse failures keep the previous
// configuration; they must never prevent loading.
func applyLifecycleConfig(request []byte) {
	if len(request) == 0 {
		return
	}
	var lifecycle lifecycleRequest
	if err := json.Unmarshal(request, &lifecycle); err != nil {
		return
	}
	masker.Configure(mask.ParseConfig(lifecycle.ConfigYAML))
}

func handleInterceptStreamChunk(request []byte) ([]byte, error) {
	var req streamChunkRequest
	if err := json.Unmarshal(request, &req); err != nil {
		// Malformed interceptor request: pass the chunk through untouched.
		return marshalResult(struct{}{})
	}
	replacement := masker.ProcessChunk(req.RequestID, req.ChunkIndex, req.Body)
	if len(replacement) == 0 {
		// Unchanged; returning an empty Body keeps the original chunk.
		return marshalResult(struct{}{})
	}
	return marshalResult(map[string]any{"Body": replacement})
}

func marshalResult(result any) ([]byte, error) {
	raw, errMarshal := json.Marshal(result)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}
