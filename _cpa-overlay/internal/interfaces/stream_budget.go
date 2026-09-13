package interfaces

import "net/http"

// UpstreamResponseTooLargeJSON is the client-facing payload emitted when an
// upstream stream exceeds its configured budget. It follows the OpenAI error
// object shape (message/type/param/code) and is already valid JSON, so
// BuildErrorResponseBody forwards it verbatim.
const UpstreamResponseTooLargeJSON = `{"error":{"message":"模型输出失控，已中止","type":"server_error","param":null,"code":"upstream_response_too_large"}}`

// StreamBudgetError marks a request that was aborted locally because the
// upstream stream exceeded its configured byte or wall-clock budget.
//
// It is terminal for the current request:
//   - StatusCode 502 makes the usage reporter record a failed record.
//   - IsRequestScoped prevents auth managers from retrying it on another credential.
//   - StreamBudgetExceeded lets the failover classifier keep it away from the
//     fallback endpoint instead of treating it as an overload error.
type StreamBudgetError struct{}

// NewStreamBudgetError returns the terminal budget-abort error.
func NewStreamBudgetError() *StreamBudgetError {
	return &StreamBudgetError{}
}

// Error returns the exact downstream payload for a budget abort.
func (e *StreamBudgetError) Error() string {
	return UpstreamResponseTooLargeJSON
}

// StatusCode reports the downstream status for a budget abort.
func (e *StreamBudgetError) StatusCode() int {
	return http.StatusBadGateway
}

// IsRequestScoped marks the error as tied to the current request so auth
// managers do not retry it across credentials.
func (e *StreamBudgetError) IsRequestScoped() bool {
	return true
}

// StreamBudgetExceeded marks the error for the failover classifier.
func (e *StreamBudgetError) StreamBudgetExceeded() bool {
	return true
}
