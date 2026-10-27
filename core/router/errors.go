package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// connectCodes are the Connect protocol's names and HTTP statuses for the
// gRPC codes.
var connectCodes = map[codes.Code]struct {
	name   string
	status int
}{
	codes.Canceled:           {"canceled", 499},
	codes.Unknown:            {"unknown", http.StatusInternalServerError},
	codes.InvalidArgument:    {"invalid_argument", http.StatusBadRequest},
	codes.DeadlineExceeded:   {"deadline_exceeded", http.StatusGatewayTimeout},
	codes.NotFound:           {"not_found", http.StatusNotFound},
	codes.AlreadyExists:      {"already_exists", http.StatusConflict},
	codes.PermissionDenied:   {"permission_denied", http.StatusForbidden},
	codes.ResourceExhausted:  {"resource_exhausted", http.StatusTooManyRequests},
	codes.FailedPrecondition: {"failed_precondition", http.StatusBadRequest},
	codes.Aborted:            {"aborted", http.StatusConflict},
	codes.OutOfRange:         {"out_of_range", http.StatusBadRequest},
	codes.Unimplemented:      {"unimplemented", http.StatusNotImplemented},
	codes.Internal:           {"internal", http.StatusInternalServerError},
	codes.Unavailable:        {"unavailable", http.StatusServiceUnavailable},
	codes.DataLoss:           {"data_loss", http.StatusInternalServerError},
	codes.Unauthenticated:    {"unauthenticated", http.StatusUnauthorized},
}

// WriteError writes err as a Connect unary error: the HTTP status for its
// code and its wireError as the body.
func WriteError(w http.ResponseWriter, err error) {
	code, e := connectError(err)
	body, _ := json.Marshal(e)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(body)
}

// wireError is a Connect error's JSON, the same for unary and streaming.
type wireError struct {
	Code    string   `json:"code"`
	Message string   `json:"message,omitempty"`
	Details []detail `json:"details,omitempty"`
}

// connectError returns err as a Connect error, with the HTTP status a unary
// call answers it with. A gRPC status error keeps its code and its details,
// each named by its message's full name with its bytes in unpadded base64,
// as connect-go writes them; a context error becomes deadline_exceeded or
// canceled; anything else is unknown.
func connectError(err error) (int, wireError) {
	st, ok := status.FromError(err)
	switch {
	case ok:
	case errors.Is(err, context.DeadlineExceeded):
		st = status.New(codes.DeadlineExceeded, err.Error())
	case errors.Is(err, context.Canceled):
		st = status.New(codes.Canceled, err.Error())
	}
	c, known := connectCodes[st.Code()]
	if !known {
		c = connectCodes[codes.Unknown]
	}
	var details []detail
	for _, d := range st.Proto().GetDetails() {
		// The message's full name, as connect-go writes it.
		name := d.GetTypeUrl()[strings.LastIndex(d.GetTypeUrl(), "/")+1:]
		details = append(details, detail{Type: name, Value: base64.RawStdEncoding.EncodeToString(d.GetValue())})
	}
	return c.status, wireError{c.name, st.Message(), details}
}

// detail is a Connect error detail.
type detail struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
