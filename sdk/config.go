package sdk

import (
	"fmt"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FieldError is one config error, keyed by the field's path, e.g.
// {"hosts[1].url", "is not a URL"}.
type FieldError struct {
	Path    string
	Message string
}

// FieldErrors collects the errors Start finds in a config.
type FieldErrors []FieldError

// Add records an error for the field at path.
func (e *FieldErrors) Add(path, format string, args ...any) {
	*e = append(*e, FieldError{Path: path, Message: fmt.Sprintf(format, args...)})
}

// Err is nil when no error was added, and otherwise the "invalid config"
// error Start returns: InvalidArgument with a google.rpc.BadRequest detail.
func (e FieldErrors) Err() error {
	if len(e) == 0 {
		return nil
	}
	br := &errdetails.BadRequest{}
	for _, fe := range e {
		br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       fe.Path,
			Description: fe.Message,
		})
	}
	st, err := status.New(codes.InvalidArgument, "invalid config").WithDetails(br)
	if err != nil {
		panic(err) // only fails for a nil detail
	}
	return st.Err()
}

// FieldErrorsOf returns the per-field errors in an "invalid config" error
// from Start, or nil for any other error.
func FieldErrorsOf(err error) FieldErrors {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		return nil
	}
	var out FieldErrors
	for _, d := range st.Details() {
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, v := range br.GetFieldViolations() {
				out = append(out, FieldError{Path: v.GetField(), Message: v.GetDescription()})
			}
		}
	}
	return out
}
