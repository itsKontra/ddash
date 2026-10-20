// A sample Component binary for the host's subprocess tests. It is named
// after the file it runs from, unless built with -ldflags "-X main.name=…".
//
// Its config's target picks how it stops: "slow-stop" takes a second to
// finish its background work once its ctx is cancelled, and "ignore-stop"
// never finishes it. Act crashes the process.
package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/itsKontra/ddash/core/internal/sample"
	"github.com/itsKontra/ddash/core/internal/sample/samplepb"
	"github.com/itsKontra/ddash/sdk"
)

// name is the name the sample says it has in Describe.
var name string

func main() {
	if name == "" {
		name = filepath.Base(os.Args[0])
	}
	sdk.Serve(sample.Define(name, nil, sample.Hooks{
		Start: func(ctx context.Context, _ *sdk.Env, cfg *samplepb.SampleConfig) error {
			switch cfg.GetTarget() {
			case "slow-stop":
				sdk.Every(ctx, time.Hour, func(ctx context.Context) {
					<-ctx.Done()
					time.Sleep(time.Second)
				})
			case "ignore-stop":
				sdk.Every(ctx, time.Hour, func(context.Context) { select {} })
			}
			return nil
		},
		Act: func(context.Context, *sdk.Env, *samplepb.ActRequest) (*samplepb.ActResponse, error) {
			os.Exit(3)
			return nil, nil
		},
	}))
}
