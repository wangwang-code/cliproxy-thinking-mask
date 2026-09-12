package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"github.com/tidwall/gjson"
)

// PendingStreamError returns an immediately available non-nil stream error.
func PendingStreamError(errs <-chan *interfaces.ErrorMessage) (*interfaces.ErrorMessage, bool) {
	if errs == nil {
		return nil, false
	}
	select {
	case errMsg, ok := <-errs:
		if ok && errMsg != nil {
			return errMsg, true
		}
	default:
	}
	return nil, false
}

type StreamForwardOptions struct {
	// KeepAliveInterval overrides the configured streaming keep-alive interval.
	// If nil, the configured default is used. If set to <= 0, keep-alives are disabled.
	KeepAliveInterval *time.Duration

	// StallTimeout overrides the configured streaming stall timeout. If nil, the
	// configured default is used. If set to <= 0, the stall timeout is disabled.
	StallTimeout *time.Duration

	// MaxStreamBytes overrides the resolved per-request upstream stream byte
	// budget. If nil, the budget is read from the gin context (set by
	// applyStreamLimits). <= 0 disables the byte cap.
	MaxStreamBytes *int

	// MaxStreamDuration overrides the resolved per-request upstream stream
	// duration cap. If nil, the budget is read from the gin context. <= 0 disables.
	MaxStreamDuration *time.Duration

	// MaxContentChars overrides the resolved per-request content-character cap.
	// If nil, the budget is read from the gin context. <= 0 disables.
	MaxContentChars *int

	// WriteChunk writes a single data chunk to the response body. It should not flush.
	WriteChunk func(chunk []byte)

	// ChunkError optionally reports that WriteChunk emitted a terminal failure.
	// The failure is passed to cancel without writing another terminal payload.
	ChunkError func() *interfaces.ErrorMessage

	// NormalizeTerminalError optionally replaces an upstream error before it is
	// written or passed to cancel.
	NormalizeTerminalError func(errMsg *interfaces.ErrorMessage) *interfaces.ErrorMessage

	// WriteTerminalError writes an error payload to the response body when streaming fails
	// after headers have already been committed. It should not flush.
	WriteTerminalError func(errMsg *interfaces.ErrorMessage)

	// CloseError optionally validates a clean upstream channel close before WriteDone.
	// Returning an error surfaces it through WriteTerminalError instead of completing the stream.
	CloseError func() *interfaces.ErrorMessage

	// WriteDone optionally writes a terminal marker when the upstream data channel closes
	// without an error (e.g. OpenAI's `[DONE]`). It should not flush.
	WriteDone func()

	// WriteKeepAlive optionally writes a keep-alive heartbeat. It should not flush.
	// When nil, a standard SSE comment heartbeat is used.
	WriteKeepAlive func()
}

func (h *BaseAPIHandler) ForwardStream(c *gin.Context, flusher http.Flusher, cancel func(error), data <-chan []byte, errs <-chan *interfaces.ErrorMessage, opts StreamForwardOptions) {
	if c == nil {
		return
	}
	if cancel == nil {
		return
	}

	writeChunk := opts.WriteChunk
	if writeChunk == nil {
		writeChunk = func([]byte) {}
	}

	writeKeepAlive := opts.WriteKeepAlive
	if writeKeepAlive == nil {
		writeKeepAlive = func() {
			_, _ = c.Writer.Write([]byte(": keep-alive\n\n"))
		}
	}

	keepAliveInterval := StreamingKeepAliveInterval(h.Cfg)
	if opts.KeepAliveInterval != nil {
		keepAliveInterval = *opts.KeepAliveInterval
	}
	var keepAlive *time.Ticker
	var keepAliveC <-chan time.Time
	if keepAliveInterval > 0 {
		keepAlive = time.NewTicker(keepAliveInterval)
		defer keepAlive.Stop()
		keepAliveC = keepAlive.C
	}

	stallTimeout := StreamingStallTimeout(h.Cfg)
	if opts.StallTimeout != nil {
		stallTimeout = *opts.StallTimeout
	}
	var stallTimer *time.Timer
	var stallC <-chan time.Time
	resetStall := func() {
		if stallTimer == nil {
			return
		}
		if !stallTimer.Stop() {
			select {
			case <-stallTimer.C:
			default:
			}
		}
		stallTimer.Reset(stallTimeout)
	}
	if stallTimeout > 0 {
		stallTimer = time.NewTimer(stallTimeout)
		defer stallTimer.Stop()
		stallC = stallTimer.C
	}

	limits, _ := streamLimitsFromContext(c)
	maxStreamBytes := limits.maxBytes
	maxStreamDuration := limits.maxDuration
	maxContentChars := limits.maxContentChars
	if opts.MaxStreamBytes != nil {
		maxStreamBytes = *opts.MaxStreamBytes
	}
	if opts.MaxStreamDuration != nil {
		maxStreamDuration = *opts.MaxStreamDuration
	}
	if opts.MaxContentChars != nil {
		maxContentChars = *opts.MaxContentChars
	}
	var streamLimitTimer *time.Timer
	var streamLimitC <-chan time.Time
	if maxStreamDuration > 0 {
		streamLimitTimer = time.NewTimer(maxStreamDuration)
		defer streamLimitTimer.Stop()
		streamLimitC = streamLimitTimer.C
	}
	streamBytes := 0
	contentChars := 0
	writeStreamLimitError := func() {
		limitErr := newUpstreamResponseTooLargeError()
		if opts.NormalizeTerminalError != nil {
			limitErr = opts.NormalizeTerminalError(limitErr)
		}
		if trackerValue, ok := c.Get(requestLifecycleContextKey); ok {
			if tracker, ok := trackerValue.(*requestLifecycleTracker); ok && tracker != nil {
				tracker.complete(pluginapi.RequestCompletionFailed, http.StatusBadGateway, limitErr.Error)
			}
		}
		if opts.WriteTerminalError != nil {
			opts.WriteTerminalError(limitErr)
			flusher.Flush()
		}
		cancel(limitErr.Error)
	}
	exceedsStreamLimits := func() bool {
		if maxStreamBytes > 0 && streamBytes > maxStreamBytes {
			return true
		}
		if maxContentChars > 0 && contentChars > maxContentChars {
			return true
		}
		return false
	}

	var terminalErr *interfaces.ErrorMessage
	for {
		select {
		case <-c.Request.Context().Done():
			cancel(c.Request.Context().Err())
			return
		case chunk, ok := <-data:
			if !ok {
				// Prefer surfacing a terminal error if one is pending.
				if terminalErr == nil {
					if errMsg, ok := PendingStreamError(errs); ok {
						terminalErr = errMsg
						if opts.NormalizeTerminalError != nil {
							terminalErr = opts.NormalizeTerminalError(terminalErr)
						}
					}
				}
				if terminalErr == nil && opts.CloseError != nil {
					terminalErr = opts.CloseError()
				}
				if terminalErr != nil {
					if opts.WriteTerminalError != nil {
						opts.WriteTerminalError(terminalErr)
					}
					flusher.Flush()
					cancel(terminalErr.Error)
					return
				}
				if opts.WriteDone != nil {
					opts.WriteDone()
				}
				flusher.Flush()
				cancel(nil)
				return
			}
			streamBytes += len(chunk)
			if maxContentChars > 0 {
				contentChars += countChunkContentChars(chunk)
			}
			if exceedsStreamLimits() {
				writeStreamLimitError()
				return
			}
			writeChunk(chunk)
			flusher.Flush()
			resetStall()
			if opts.ChunkError != nil {
				chunkErr := opts.ChunkError()
				if chunkErr != nil {
					if opts.NormalizeTerminalError != nil {
						chunkErr = opts.NormalizeTerminalError(chunkErr)
					}
					if chunkErr != nil {
						cancel(chunkErr.Error)
					} else {
						cancel(nil)
					}
					return
				}
			}
		case errMsg, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if errMsg != nil {
				terminalErr = errMsg
				if opts.NormalizeTerminalError != nil {
					terminalErr = opts.NormalizeTerminalError(terminalErr)
				}
				if opts.WriteTerminalError != nil {
					opts.WriteTerminalError(terminalErr)
					flusher.Flush()
				}
			}
			var execErr error
			if terminalErr != nil {
				execErr = terminalErr.Error
			}
			cancel(execErr)
			return
		case <-streamLimitC:
			writeStreamLimitError()
			return
		case <-stallC:
			stallErr := &interfaces.ErrorMessage{
				StatusCode: http.StatusGatewayTimeout,
				Error:      fmt.Errorf("upstream stream stalled after %s", stallTimeout),
			}
			if opts.NormalizeTerminalError != nil {
				stallErr = opts.NormalizeTerminalError(stallErr)
			}
			if opts.WriteTerminalError != nil {
				opts.WriteTerminalError(stallErr)
				flusher.Flush()
			}
			cancel(stallErr.Error)
			return
		case <-keepAliveC:
			writeKeepAlive()
			flusher.Flush()
		}
	}
}

// countChunkContentChars returns the number of content characters in one
// OpenAI/Claude-style streaming chunk. It is only called when a content cap is
// configured.
func countChunkContentChars(chunk []byte) int {
	if len(chunk) == 0 {
		return 0
	}
	content := gjson.GetBytes(chunk, "choices.0.delta.content")
	if content.Type == gjson.String {
		return len([]rune(content.String()))
	}
	content = gjson.GetBytes(chunk, "choices.0.message.content")
	if content.Type == gjson.String {
		return len([]rune(content.String()))
	}
	return 0
}
