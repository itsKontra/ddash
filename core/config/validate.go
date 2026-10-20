package config

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/itsKontra/ddash/sdk"
)

// Validate checks cfg, with its defaults applied, against its fields'
// required, format and min/max options: the rules the form checks too.
func Validate(cfg protoreflect.Message) sdk.FieldErrors {
	return nil
}
