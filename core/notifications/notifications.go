// Package notifications is the core's notification service (ADR-0010).
// Every Component sends Notifications through it, whatever it requires:
// it is served on every Component's host server (router.Offer). Each one
// goes to every installed channel, a Component whose API is
// ddash.notification.ChannelService, that takes its category: all at
// once, each within DeliveryDeadline, so a channel that fails or hangs
// never holds up another. The core knows no channel by name; installing a
// channel's Plugin is what adds it.
package notifications

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/itsKontra/ddash/core/appstate"
	"github.com/itsKontra/ddash/core/auth"
	"github.com/itsKontra/ddash/core/router"
	"github.com/itsKontra/ddash/core/runtime"
	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"
)

// DeliveryDeadline bounds how long Send waits for one channel: its
// GetChannel and its Deliver. A Deliver still running then goes on to its
// end, as every admitted Action does (ADR-0004), and its Audit Log entry
// says how it ended; Send reports it failed. A variable for tests.
var DeliveryDeadline = 20 * time.Second

// Runtime is the runtime as the service sees it.
type Runtime interface {
	// Serving returns, sorted, the wanted Components whose API is service.
	Serving(service protoreflect.FullName) []string
	// Status returns every wanted Component's state, by name.
	Status() map[string]runtime.Status
	// CheckHealth has a Component's health polled now.
	CheckHealth(ctx context.Context, name string)
}

// Service is the core's notification service.
type Service struct {
	runtime Runtime
	router  *router.Router
	gate    *appstate.Gate
	log     *slog.Logger
}

// New returns the service, finding channels in rt, calling them through
// rtr and auditing every Send at gate.
func New(rt Runtime, rtr *router.Router, gate *appstate.Gate, log *slog.Logger) *Service {
	return &Service{runtime: rt, router: rtr, gate: gate, log: log}
}

var (
	file = notificationpb.File_ddash_notification_notification_proto
	// sendMethod is what the Audit Log records a Send as.
	sendMethod = file.Services().ByName("NotificationService").Methods().ByName("Send")
	// channelService is the API every channel serves.
	channelService = file.Services().ByName("ChannelService").FullName()
)

// categories are every Category a Notification may have.
var categories = []notificationpb.Category{
	notificationpb.Category_CATEGORY_CHECKS,
	notificationpb.Category_CATEGORY_IMAGE_UPDATES,
	notificationpb.Category_CATEGORY_ACTION_OUTCOMES,
}

// Register serves the service to caller on s, for router.Offer.
func (s *Service) Register(srv grpc.ServiceRegistrar, caller string) {
	notificationpb.RegisterNotificationServiceServer(srv, server{s: s, caller: caller})
}

// server is the service as one Component reaches it.
type server struct {
	notificationpb.UnimplementedNotificationServiceServer
	s      *Service
	caller string
}

func (c server) Send(ctx context.Context, req *notificationpb.SendRequest) (*notificationpb.SendResponse, error) {
	actor, chain := router.Forwarded(ctx, c.caller)
	return c.s.Send(ctx, actor, chain, req)
}

func (c server) ListChannels(ctx context.Context, _ *notificationpb.ListChannelsRequest) (*notificationpb.ListChannelsResponse, error) {
	actor, chain := router.Forwarded(ctx, c.caller)
	return c.s.ListChannels(ctx, actor, chain)
}

// Send is the core Action that delivers req's Notification, run for actor
// as a hop of chain: it is recorded in the Audit Log (target core), and so
// is each channel's Deliver after it, in the same chain. It refuses a
// Notification without a category (InvalidArgument), and fails
// (Unavailable) only when every channel that should have received it
// failed. Once admitted, it runs detached to its end (ADR-0004).
func (s *Service) Send(ctx context.Context, actor auth.Actor, chain string, req *notificationpb.SendRequest) (*notificationpb.SendResponse, error) {
	var refused error
	if !slices.Contains(categories, req.GetCategory()) {
		refused = status.Error(codes.InvalidArgument, "a Notification needs a category")
	}
	done, err := s.gate.Admit(ctx, actor, appstate.Action{Chain: chain, Target: "core", Method: sendMethod, Request: req})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		done(refused)
		return nil, refused
	}
	ctx, cancel := router.Detach(ctx)
	defer cancel()
	resp, err := s.deliver(ctx, actor, chain, req)
	done(err)
	return resp, err
}

// deliver hands req's Notification to every installed channel at once and
// waits for each at most DeliveryDeadline.
func (s *Service) deliver(ctx context.Context, actor auth.Actor, chain string, req *notificationpb.SendRequest) (*notificationpb.SendResponse, error) {
	names := s.runtime.Serving(channelService)
	deliveries := make([]*notificationpb.Delivery, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() { deliveries[i] = s.deliverTo(ctx, actor, chain, name, req) })
	}
	wg.Wait()

	var sent bool
	var failed []string
	for _, d := range deliveries {
		switch d.GetState() {
		case notificationpb.DeliveryState_DELIVERY_STATE_SENT:
			sent = true
		case notificationpb.DeliveryState_DELIVERY_STATE_FAILED:
			failed = append(failed, d.GetChannel()+": "+d.GetError())
			s.log.Warn("deliver Notification", "channel", d.GetChannel(), "title", req.GetNotification().GetTitle(), "err", d.GetError())
		}
	}
	if !sent && len(failed) > 0 {
		return nil, status.Error(codes.Unavailable, strings.Join(failed, "; "))
	}
	return &notificationpb.SendResponse{Deliveries: deliveries}, nil
}

// deliverTo asks channel which categories it takes and, when it takes
// req's, has it deliver the Notification, within DeliveryDeadline.
func (s *Service) deliverTo(ctx context.Context, actor auth.Actor, chain, channel string, req *notificationpb.SendRequest) *notificationpb.Delivery {
	failed := func(err error) *notificationpb.Delivery {
		return &notificationpb.Delivery{Channel: channel, State: notificationpb.DeliveryState_DELIVERY_STATE_FAILED, Error: status.Convert(err).Message()}
	}
	result := make(chan *notificationpb.Delivery, 1)
	go func() {
		var ch notificationpb.GetChannelResponse
		if err := s.router.Hop(ctx, actor, chain, channel, "GetChannel", &notificationpb.GetChannelRequest{}, &ch); err != nil {
			result <- failed(err)
			return
		}
		if !slices.Contains(ch.GetCategories(), req.GetCategory()) {
			result <- &notificationpb.Delivery{Channel: channel, State: notificationpb.DeliveryState_DELIVERY_STATE_FILTERED}
			return
		}
		in := &notificationpb.DeliverRequest{Notification: req.GetNotification(), Category: req.GetCategory()}
		err := s.router.Hop(ctx, actor, chain, channel, "Deliver", in, &notificationpb.DeliverResponse{})
		// Its health is its last delivery's outcome: its Plugin shows it now.
		s.runtime.CheckHealth(ctx, channel)
		if err != nil {
			result <- failed(err)
			return
		}
		result <- &notificationpb.Delivery{Channel: channel, State: notificationpb.DeliveryState_DELIVERY_STATE_SENT}
	}()
	timer := time.NewTimer(DeliveryDeadline)
	defer timer.Stop()
	select {
	case d := <-result:
		return d
	case <-timer.C:
		return failed(status.Errorf(codes.DeadlineExceeded, "no answer within %s", DeliveryDeadline))
	}
}

// ListChannels reports every installed channel, by Component name: what it
// says it is, and its problem when its Component isn't up or not ok (its
// last delivery failed).
func (s *Service) ListChannels(ctx context.Context, actor auth.Actor, chain string) (*notificationpb.ListChannelsResponse, error) {
	names := s.runtime.Serving(channelService)
	states := s.runtime.Status()
	out := make([]*notificationpb.Channel, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		c := &notificationpb.Channel{Component: name, Title: name}
		out[i] = c
		wg.Go(func() {
			var ch notificationpb.GetChannelResponse
			if err := s.router.Hop(ctx, actor, chain, name, "GetChannel", &notificationpb.GetChannelRequest{}, &ch); err != nil {
				c.Problem = status.Convert(err).Message()
				return
			}
			c.Title, c.Destination, c.Categories = ch.GetTitle(), ch.GetDestination(), ch.GetCategories()
			if st := states[name]; st.State != runtime.OK && st.State != runtime.Starting {
				c.Problem = string(st.State)
				if st.Message != "" {
					c.Problem = st.Message
				}
			}
		})
	}
	wg.Wait()
	return &notificationpb.ListChannelsResponse{Channels: out}, nil
}
