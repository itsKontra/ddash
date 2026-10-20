package sdk

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
)

func (d Definition) describe() (*componentpb.DescribeResponse, error) {
	svc, err := d.serviceDescriptor()
	if err != nil {
		return nil, err
	}
	cfg := d.newConfig().ProtoReflect().Descriptor()
	set := &descriptorpb.FileDescriptorSet{}
	seen := map[string]bool{}
	addFile(set, seen, svc.ParentFile())
	addFile(set, seen, cfg.ParentFile())
	return &componentpb.DescribeResponse{
		Name:          d.manifest.Name,
		Release:       Release,
		Requires:      d.manifest.Requires,
		Service:       string(svc.FullName()),
		ConfigMessage: string(cfg.FullName()),
		Descriptors:   set,
	}, nil
}

func (d Definition) serviceDescriptor() (protoreflect.ServiceDescriptor, error) {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(d.service.ServiceName))
	if err != nil {
		return nil, fmt.Errorf("sdk: %s: service %s: %w", d.manifest.Name, d.service.ServiceName, err)
	}
	svc, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("sdk: %s: %s is not a service", d.manifest.Name, d.service.ServiceName)
	}
	return svc, nil
}

// addFile appends f after everything it imports, once.
func addFile(set *descriptorpb.FileDescriptorSet, seen map[string]bool, f protoreflect.FileDescriptor) {
	if seen[f.Path()] {
		return
	}
	seen[f.Path()] = true
	imports := f.Imports()
	for i := range imports.Len() {
		addFile(set, seen, imports.Get(i).FileDescriptor)
	}
	set.File = append(set.File, protodesc.ToFileDescriptorProto(f))
}
