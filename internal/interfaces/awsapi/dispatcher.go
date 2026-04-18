package awsapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
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
	"ListMatchmakingConfigurations":     (*Server).handleDescribeConfigurations,
	"DescribeMatchmakingRuleSets":       (*Server).handleDescribeRuleSets,
	"ListMatchmakingRuleSets":           (*Server).handleDescribeRuleSets,
	"ValidateMatchmakingRuleSet":        (*Server).handleValidateRuleSet,
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	action := strings.TrimPrefix(target, "GameLift.")
	if action == "" || action == target {
		newInvalidRequest("missing or malformed X-Amz-Target header").write(w)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		newInvalidRequest("read request body: %v", err).write(w)
		return
	}
	h, ok := handlers[action]
	if !ok {
		newUnknownOperation("unknown action %q", action).write(w)
		return
	}
	out, err := h(s, r, body)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			apiErr.write(w)
			return
		}
		if mapped := translateDomainError(err); mapped != nil {
			mapped.write(w)
			return
		}
		s.logger.Error("handler error", "action", action, "err", err.Error())
		newInternal("handler %q failed: %v", action, err).write(w)
		return
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	if out == nil {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}

func decodeJSON(body []byte, dst any) error {
	if len(body) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return newInvalidRequest("parse json body: %v", err)
	}
	return nil
}
