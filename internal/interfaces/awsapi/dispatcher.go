package awsapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type handler func(*Server, *http.Request, []byte) (any, error)

var handlers = map[string]handler{
	"StartMatchmaking":                  (*Server).handleStartMatchmaking,
	"StopMatchmaking":                   (*Server).handleStopMatchmaking,
	"DescribeMatchmaking":               (*Server).handleDescribeMatchmaking,
	"AcceptMatch":                       (*Server).handleAcceptMatch,
	"StartMatchBackfill":                (*Server).handleStartMatchBackfill,
	"StopMatchBackfill":                 (*Server).handleStopMatchBackfill,
	"DescribeMatchmakingConfigurations": (*Server).handleDescribeConfigurations,
	"DescribeMatchmakingRuleSets":       (*Server).handleDescribeRuleSets,
	"ValidateMatchmakingRuleSet":        (*Server).handleValidateRuleSet,
}

// wireFormat captures the encoding-specific output behavior shared by the JSON
// (aws-json-1.1) and CBOR (rpc-v2-cbor) dispatch paths. serve drives the common
// flow — handler lookup, execution, error classification, request logging — and
// defers to the format only for rendering the error or success response.
type wireFormat struct {
	writeErr func(*APIError, http.ResponseWriter)
	writeOK  func(*Server, http.ResponseWriter, string, any)
}

var (
	jsonFormat = wireFormat{writeErr: (*APIError).write, writeOK: (*Server).writeJSONResponse}
	cborFormat = wireFormat{writeErr: (*APIError).writeCBOR, writeOK: (*Server).writeCBORResponse}
)

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	target := r.Header.Get("X-Amz-Target")
	action, ok := strings.CutPrefix(target, "GameLift.")
	if !ok || action == "" {
		newInvalidRequest("missing or malformed X-Amz-Target header").write(w)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		newInvalidRequest("read request body: %v", err).write(w)
		return
	}
	s.serve(w, r, action, body, start, jsonFormat)
}

func (s *Server) dispatchCBOR(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	action := r.PathValue("action")
	if action == "" {
		newInvalidRequest("missing action in URL path").writeCBOR(w)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		newInvalidRequest("read request body: %v", err).writeCBOR(w)
		return
	}
	jsonBody, err := cborBodyToJSON(body)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			apiErr.writeCBOR(w)
			return
		}
		newInvalidRequest("convert cbor body: %v", err).writeCBOR(w)
		return
	}
	s.serve(w, r, action, jsonBody, start, cborFormat)
}

// serve runs the decoded request against its handler and renders the result
// through the given wire format. body is always JSON-encoded here; the CBOR path
// converts to JSON before calling so handlers stay format-agnostic.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, action string, body []byte, start time.Time, f wireFormat) {
	h, ok := handlers[action]
	if !ok {
		f.writeErr(newUnknownOperation("unknown action %q", action), w)
		return
	}
	out, err := h(s, r, body)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			s.logRequest(action, apiErr.HTTPStatus, start)
			f.writeErr(apiErr, w)
			return
		}
		if mapped := translateDomainError(err); mapped != nil {
			s.logRequest(action, mapped.HTTPStatus, start)
			f.writeErr(mapped, w)
			return
		}
		s.logger.Error("handler error", "action", action, "err", err.Error())
		f.writeErr(newInternal("handler %q failed: %v", action, err), w)
		return
	}
	s.logRequest(action, http.StatusOK, start)
	f.writeOK(s, w, action, out)
}

func (s *Server) logRequest(action string, status int, start time.Time) {
	s.logger.Debug("api request", "action", action, "status", status, "duration_ms", time.Since(start).Milliseconds())
}

// writeJSONResponse renders a successful handler result as aws-json-1.1. action
// is unused but kept for symmetry with writeCBORResponse so both satisfy
// wireFormat.writeOK.
func (s *Server) writeJSONResponse(w http.ResponseWriter, _ string, out any) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("smithy-protocol", "aws-json-1.1")
	w.WriteHeader(http.StatusOK)
	if out == nil {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}

// writeCBORResponse renders a successful handler result as rpc-v2-cbor.
func (s *Server) writeCBORResponse(w http.ResponseWriter, action string, out any) {
	w.Header().Set("Content-Type", "application/cbor")
	w.Header().Set("smithy-protocol", "rpc-v2-cbor")
	w.WriteHeader(http.StatusOK)
	if out == nil {
		return
	}
	cborBytes, err := encodeCBOR(out)
	if err != nil {
		s.logger.Error("cbor encode error", "action", action, "err", err.Error())
		return
	}
	_, _ = w.Write(cborBytes)
}

func decodeJSON(body []byte, dst any) error {
	if len(body) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return newInvalidRequest("parse json body: %v", err)
	}
	return nil
}
