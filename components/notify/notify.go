// Package notify is the notify Component: it sends each Notification to
// every channel (ntfy-api, telegram-api) that is set up and whose filter
// takes its category, all at once. The filters are its config. A channel
// not set up (its Component unconfigured, or Telegram without a bot) is
// skipped, never an error; Send fails only when every channel that should
// have received the Notification failed.
package notify

import (
	"context"
	_ "embed"
	"log/slog"
	"slices"
	"strings"
	"sync"

	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/notify/notifypb"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/components/telegram-api/telegramapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// Definition is notify, ready for sdk.Serve.
func Definition() sdk.Definition {
	return sdk.Define(sdk.Spec[*notifypb.NotifyConfig]{
		Manifest: manifest,
		Service:  &notifypb.NotifyService_ServiceDesc,
		New:      func() sdk.Component[*notifypb.NotifyConfig] { return &component{} },
	})
}

type component struct {
	notifypb.UnimplementedNotifyServiceServer
	log *slog.Logger
	// channels in the order Deliveries and ListChannels report them.
	channels []*channel
}

// channel is one way a Notification reaches the Operator.
type channel struct {
	name, title, component string
	// takes is the categories its filter takes.
	takes []notifypb.Category
	send  func(context.Context, *notificationpb.Notification) error
	// describe says whether it is set up and where it sends to.
	describe func(context.Context) (setUp bool, destination string, err error)
}

// categories are every Category, in the order ListChannels reports them.
var categories = []notifypb.Category{
	notifypb.Category_CATEGORY_CHECKS,
	notifypb.Category_CATEGORY_IMAGE_UPDATES,
	notifypb.Category_CATEGORY_ACTION_OUTCOMES,
}

// filter is the categories whose switch, in categories' order, is on; an
// unset switch is on.
func filter(on ...*bool) []notifypb.Category {
	var takes []notifypb.Category
	for i, b := range on {
		if b == nil || *b {
			takes = append(takes, categories[i])
		}
	}
	return takes
}

func (c *component) Start(_ context.Context, env *sdk.Env, cfg *notifypb.NotifyConfig) error {
	c.log = env.Log
	ntfy := sdk.Require(env, ntfyapipb.NewNtfyApiServiceClient)
	telegram := sdk.Require(env, telegramapipb.NewTelegramApiServiceClient)
	c.channels = []*channel{
		{
			name: "ntfy", title: "ntfy", component: "ntfy-api",
			takes: filter(cfg.NtfyChecks, cfg.NtfyImageUpdates, cfg.NtfyActionOutcomes),
			send: func(ctx context.Context, n *notificationpb.Notification) error {
				_, err := ntfy.Send(ctx, &ntfyapipb.SendRequest{Notification: n})
				return err
			},
			describe: func(ctx context.Context) (bool, string, error) {
				ch, err := ntfy.GetChannel(ctx, &ntfyapipb.GetChannelRequest{})
				return ch.GetSetUp(), ch.GetDestination(), err
			},
		},
		{
			name: "telegram", title: "Telegram", component: "telegram-api",
			takes: filter(cfg.TelegramChecks, cfg.TelegramImageUpdates, cfg.TelegramActionOutcomes),
			send: func(ctx context.Context, n *notificationpb.Notification) error {
				_, err := telegram.Send(ctx, &telegramapipb.SendRequest{Notification: n})
				return err
			},
			describe: func(ctx context.Context) (bool, string, error) {
				ch, err := telegram.GetChannel(ctx, &telegramapipb.GetChannelRequest{})
				return ch.GetSetUp(), ch.GetDestination(), err
			},
		},
	}
	return nil
}

func (c *component) Health(context.Context) sdk.Health { return sdk.Health{State: sdk.OK} }

// notSetUp reports whether err is a channel's answer while it isn't set
// up: the host's "not configured" for an unconfigured Component, or the
// Component's own refusal (telegram-api without a bot).
func notSetUp(err error) bool { return status.Code(err) == codes.FailedPrecondition }

func (c *component) Send(ctx context.Context, req *notifypb.SendRequest) (*notifypb.SendResponse, error) {
	n := req.GetNotification()
	if !slices.Contains(categories, req.GetCategory()) {
		return nil, status.Error(codes.InvalidArgument, "a Notification needs a category")
	}
	deliveries := make([]*notifypb.Delivery, len(c.channels))
	var wg sync.WaitGroup
	for i, ch := range c.channels {
		d := &notifypb.Delivery{Channel: ch.name, State: notifypb.DeliveryState_DELIVERY_STATE_FILTERED}
		deliveries[i] = d
		if !slices.Contains(ch.takes, req.GetCategory()) {
			continue
		}
		wg.Go(func() {
			switch err := ch.send(ctx, n); {
			case err == nil:
				d.State = notifypb.DeliveryState_DELIVERY_STATE_SENT
			case notSetUp(err):
				d.State = notifypb.DeliveryState_DELIVERY_STATE_NOT_SET_UP
			default:
				d.State, d.Error = notifypb.DeliveryState_DELIVERY_STATE_FAILED, status.Convert(err).Message()
			}
		})
	}
	wg.Wait()

	var sent bool
	var failed []string
	for i, d := range deliveries {
		switch d.GetState() {
		case notifypb.DeliveryState_DELIVERY_STATE_SENT:
			sent = true
		case notifypb.DeliveryState_DELIVERY_STATE_FAILED:
			failed = append(failed, c.channels[i].title+": "+d.GetError())
			c.log.Warn("send Notification", "channel", d.GetChannel(), "title", n.GetTitle(), "err", d.GetError())
		}
	}
	if !sent && len(failed) > 0 {
		return nil, status.Error(codes.Unavailable, strings.Join(failed, "; "))
	}
	return &notifypb.SendResponse{Deliveries: deliveries}, nil
}

func (c *component) ListChannels(ctx context.Context, _ *notifypb.ListChannelsRequest) (*notifypb.ListChannelsResponse, error) {
	out := make([]*notifypb.Channel, len(c.channels))
	var wg sync.WaitGroup
	for i, ch := range c.channels {
		o := &notifypb.Channel{Name: ch.name, Title: ch.title, Component: ch.component, Categories: ch.takes}
		out[i] = o
		wg.Go(func() {
			setUp, dest, err := ch.describe(ctx)
			switch {
			case err == nil:
				o.SetUp, o.Destination = setUp, dest
			case !notSetUp(err):
				o.Problem = ch.component + ": " + status.Convert(err).Message()
			}
		})
	}
	wg.Wait()
	return &notifypb.ListChannelsResponse{Channels: out}, nil
}
