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
// code and a JSON body {code, message, details}. A gRPC status error keeps
// its code and its details, each named by its message's full name with
// its bytes in unpadded base64, as connect-go writes them; a context error
// becomes deadline_exceeded or canceled; anything else is unknown.
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
	var details []detail
	for _, d := range st.Proto().GetDetails() {
		// The message's full name, as connect-go writes it.
		name := d.GetTypeUrl()[strings.LastIndex(d.GetTypeUrl(), "/")+1:]
		details = append(details, detail{Type: name, Value: base64.RawStdEncoding.EncodeToString(d.GetValue())})
	}
	body, _ := json.Marshal(struct {
		Code    string   `json:"code"`
		Message string   `json:"message,omitempty"`
		Details []detail `json:"details,omitempty"`
	}{c.name, st.Message(), details})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(c.status)
	w.Write(body)
}

// detail is a Connect error detail.
type detail struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
