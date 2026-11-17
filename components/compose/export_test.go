package compose

import (
	pb "github.com/itsKontra/ddash/components/compose/composepb"
	containerpb "github.com/itsKontra/ddash/proto/ddash/container"
)

// ImageStatusOf is the image state compose gives ct before any check.
func ImageStatusOf(ct *containerpb.Container) *pb.ImageStatus {
	return (&component{}).status(ct)
}
