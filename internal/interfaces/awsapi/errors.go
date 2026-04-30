package awsapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	mm "github.com/moepig/fmlocal/internal/domain/matchmaking"
)

type APIError struct {
	TypeName   string
	Message    string
	HTTPStatus int
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: %s", e.TypeName, e.Message) }

func (e *APIError) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("smithy-protocol", "aws-json-1.1")
	if e.HTTPStatus == 0 {
		e.HTTPStatus = http.StatusBadRequest
	}
	w.WriteHeader(e.HTTPStatus)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type":  e.TypeName,
		"message": e.Message,
	})
}

func newInvalidRequest(format string, args ...any) *APIError {
	return &APIError{TypeName: "InvalidRequestException", Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}

func newNotFound(format string, args ...any) *APIError {
	return &APIError{TypeName: "NotFoundException", Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}

func newUnsupported(format string, args ...any) *APIError {
	return &APIError{TypeName: "UnsupportedOperationException", Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}

func newUnknownOperation(format string, args ...any) *APIError {
	return &APIError{TypeName: "UnknownOperationException", Message: fmt.Sprintf(format, args...), HTTPStatus: 400}
}

func newInternal(format string, args ...any) *APIError {
	return &APIError{TypeName: "InternalServiceException", Message: fmt.Sprintf(format, args...), HTTPStatus: 500}
}

func translateDomainError(err error) *APIError {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, mm.ErrTicketNotFound),
		errors.Is(err, mm.ErrConfigurationNotFound),
		errors.Is(err, mm.ErrRuleSetNotFound):
		return newNotFound("%v", err)
	case errors.Is(err, mm.ErrTicketAlreadyExists),
		errors.Is(err, mm.ErrPlayerNotInTicket),
		errors.Is(err, mm.ErrProposalNotFound),
		errors.Is(err, mm.ErrInvalidTransition),
		errors.Is(err, mm.ErrInvalidRuleSet):
		return newInvalidRequest("%v", err)
	case errors.Is(err, mm.ErrBackfillUnsupported):
		return newUnsupported("%v", err)
	}
	return newInternal("%v", err)
}
