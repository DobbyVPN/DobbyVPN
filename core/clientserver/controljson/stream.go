package controljson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"core/log"
	"core/sessionapi/wire"
)

// ReadServiceLogsMethod serves the service's own retained log generations (and
// their byte offsets) over the authenticated local endpoint. Unlike the
// request/response commands, its response line is followed by a raw byte tail
// of exactly StreamResult.Bytes bytes, because a retained generation can
// exceed the 8 MiB response line limit.
const ReadServiceLogsMethod = "ReadServiceLogs"

// StreamSegment describes one captured service-log generation and the byte
// range its tail carries. ID is the captured input's stable identity.
type StreamSegment struct {
	Path   string `json:"path"`
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
}

// StreamResult is the response line of a ReadServiceLogs call.
type StreamResult struct {
	Segments []StreamSegment `json:"segments"`
	Bytes    int64           `json:"bytes"`
}

// StreamExport is a captured read. Write emits exactly Result.Bytes bytes in
// Segments order; Close releases the capture.
type StreamExport struct {
	Result StreamResult
	Write  func(w io.Writer) error
	Close  func() error
}

// StreamLogsFunc captures the service's retained log for one request. since
// maps an input identity to the first byte wanted (absent means zero);
// metadataOnly omits the tail, for view-boundary callers that need only sizes.
type StreamLogsFunc func(ctx context.Context, since map[string]int64, metadataOnly bool) (*StreamExport, error)

type readServiceLogsParams struct {
	Since        map[string]int64 `json:"since"`
	MetadataOnly bool             `json:"metadata_only"`
}

// serveReadServiceLogs answers one streaming request: the JSON response line
// announces the segments and the byte count, then the raw tail follows.
func (h Handler) serveReadServiceLogs(conn net.Conn, ctx context.Context, request Request) error {
	fields := requestFields(requestIdentity(request.RequestID), request.Method, request.Params)
	var params readServiceLogsParams
	if len(request.Params) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(request.Params))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&params); err != nil {
			controlFailure(fields, "decode", err)
			return writeFailureWithCause(conn, invalidArgumentCode, "command request is invalid: "+err.Error(), err)
		}
	}
	if h.StreamLogs == nil {
		err := errors.New("service log source is unavailable")
		controlFailure(fields, "binding", err)
		return writeFailure(conn, invalidArgumentCode, "unknown command")
	}
	started := time.Now()
	log.Info(controlCategory, "desktop command accepted", fields)
	export, err := h.StreamLogs(ctx, params.Since, params.MetadataOnly)
	if err != nil {
		controlFailure(fields, "capture", err)
		return writeFailureWithCause(conn, "INTERNAL", "service log capture failed: "+err.Error(), err)
	}
	defer func() { _ = export.Close() }()
	response, err := json.Marshal(wire.Response[StreamResult]{OK: true, Result: export.Result})
	if err != nil {
		controlFailure(fields, "encode", err)
		return writeFailureWithCause(conn, "INTERNAL", "desktop control could not encode the response: "+err.Error(), err)
	}
	if _, err := conn.Write(append(response, '\n')); err != nil {
		controlFailure(fields, "write", err)
		return err
	}
	if !params.MetadataOnly && export.Result.Bytes > 0 {
		if err := export.Write(conn); err != nil {
			controlFailure(fields, "stream", err)
			return err
		}
	}
	fields["elapsed_ms"] = time.Since(started).Milliseconds()
	fields["segments"] = len(export.Result.Segments)
	fields["stream_bytes"] = export.Result.Bytes
	log.Info(controlCategory, "desktop command completed", fields)
	return nil
}
