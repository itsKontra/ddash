package sdk_test

import (
	"testing"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	componentpb "github.com/itsKontra/ddash/proto/ddash/component"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/internal/echo/echopb"
)

// The host dispenses a Component through go-plugin and gets one gRPC
// connection serving both its lifecycle and its API.
func TestAComponentIsServedThroughGoPlugin(t *testing.T) {
	inst := sdk.NewInstance(define(&echo{}), sdk.InstanceOptions{})
	t.Cleanup(func() { inst.Stop(t.Context()) })
	client, _ := plugin.TestPluginGRPCConn(t, false, sdk.Plugins(inst))
	t.Cleanup(func() { client.Close() })

	raw, err := client.Dispense(sdk.PluginName)
	if err != nil {
		t.Fatal(err)
	}
	conn := raw.(grpc.ClientConnInterface)
	lifecycle := componentpb.NewLifecycleServiceClient(conn)

	d, err := lifecycle.Describe(t.Context(), &componentpb.DescribeRequest{})
	if err != nil || d.GetName() != "echo" {
		t.Fatalf("Describe = %v, %v", d, err)
	}
	cfg, _ := proto.Marshal(&echopb.EchoConfig{Prefix: "> "})
	if _, err := lifecycle.Start(t.Context(), &componentpb.StartRequest{Config: cfg}); err != nil {
		t.Fatal(err)
	}
	resp, err := echopb.NewEchoServiceClient(conn).Echo(t.Context(), &echopb.EchoRequest{Message: "hi"})
	if err != nil || resp.GetMessage() != "> hi" {
		t.Fatalf("Echo = %q, %v", resp.GetMessage(), err)
	}
}
