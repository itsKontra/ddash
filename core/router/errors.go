package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

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
// code and a JSON body {code, message}. A gRPC status error keeps its code;
// a context error becomes deadline_exceeded or canceled; anything else is
// unknown.
func WriteError(w http.ResponseWriter, err error) {
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
	body, _ := json.Marshal(struct {
		Code    string `json:"code"`
		Message string `json:"message,omitempty"`
	}{c.name, st.Message()})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c.status)
	w.Write(body)
}
