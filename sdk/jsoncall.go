package sdk

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/sdk/jsoncall"
)

// Unanswered is the gRPC error for a jsoncall.Do that has no Reply from
// system (as the Operator knows it, e.g. "pfSense"): Unavailable when it
// didn't answer or its answer broke off, Internal when the request
// couldn't be made. ADR-0007 has what each code means.
func Unanswered(err error, system string) error {
	var call *jsoncall.Error
	if !errors.As(err, &call) {
		return status.Errorf(codes.Unavailable, "%s: %v", system, err)
	}
	switch call.Stage {
	case jsoncall.Build:
		return status.Errorf(codes.Internal, "can't make the request to %s: %v", system, call.Err)
	case jsoncall.Read:
		return status.Errorf(codes.Unavailable, "%s's answer broke off: %v", system, call.Cause())
	}
	return status.Errorf(codes.Unavailable, "%s unreachable: %v", system, call.Cause())
}
