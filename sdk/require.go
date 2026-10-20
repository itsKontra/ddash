package sdk

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Require returns the generated client of a Component this one lists in
// requires, e.g. sdk.Require(env, checkspb.NewChecksServiceClient). Calls go
// through the host, which checks requires, sets deadlines and gates Actions.
// In tests it returns the fake given to sdktest.WithClient.
func Require[T any](env *Env, newClient func(grpc.ClientConnInterface) T) T {
	for _, c := range env.clients {
		if fake, ok := c.(T); ok {
			return fake
		}
	}
	if env.host == nil {
		return newClient(noHost{})
	}
	return newClient(env.host)
}

// noHost fails every call: there is no host connection to send it through.
type noHost struct{}

func (noHost) Invoke(_ context.Context, method string, _, _ any, _ ...grpc.CallOption) error {
	return status.Errorf(codes.Unavailable, "%s: no connection to the host", method)
}

func (noHost) NewStream(_ context.Context, _ *grpc.StreamDesc, method string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, status.Errorf(codes.Unavailable, "%s: no connection to the host", method)
}
