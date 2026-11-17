// Package ntfyapi is the ntfy-api Component, the ntfy channel: it serves
// ddash.notification.ChannelService and publishes each Notification the
// core delivers to one ntfy topic as JSON to the server's root, with the
// topic's access token as a Bearer token. Its health is the last
// delivery's outcome.
package ntfyapi

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	notificationpb "github.com/itsKontra/ddash/proto/ddash/notification"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/internal/channel"
	"github.com/itsKontra/ddash/components/ntfy-api/ntfyapipb"
	"github.com/itsKontra/ddash/sdk"
)

//go:embed component.yaml
var manifest []byte

// publishTimeout bounds one publish.
const publishTimeout = 10 * time.Second

// Definition is ntfy-api, ready for sdk.Serve.
func Definition() sdk.Definition {
	return define(&http.Client{Timeout: publishTimeout})
}

// define is ntfy-api publishing through client.
func define(client *http.Client) sdk.Definition {
	return sdk.Define(sdk.Spec[*ntfyapipb.NtfyApiConfig]{
		Manifest: manifest,
		Service:  &notificationpb.ChannelService_ServiceDesc,
		New:      func() sdk.Component[*ntfyapipb.NtfyApiConfig] { return &component{client: client} },
	})
}

type component struct {
	notificationpb.UnimplementedChannelServiceServer
	client *http.Client

	// root is the server's URL with a trailing slash: JSON is published there.
	root, topic, token string
	// host is the server's host, for GetChannel.
	host string
	// takes is the categories the filter takes.
	takes []notificationpb.Category

	mu sync.Mutex
	// lastErr is why the last delivery failed; nil once one succeeds.
	lastErr error
}

var topicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

func (c *component) Start(_ context.Context, _ *sdk.Env, cfg *ntfyapipb.NtfyApiConfig) error {
	var errs sdk.FieldErrors
	server := cfg.GetServer()
	if cfg.Server == nil {
		// The host fills in the field's default; a test may not.
		server = "https://ntfy.sh"
	}
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs.Add("server", "must be an http or https URL")
	}
	if !topicPattern.MatchString(cfg.GetTopic()) {
		errs.Add("topic", "must be 1 to 64 letters, digits, - or _")
	}
	if err := errs.Err(); err != nil {
		return err
	}
	c.root = strings.TrimSuffix(u.String(), "/") + "/"
	c.topic, c.token, c.host = cfg.GetTopic(), cfg.GetToken(), u.Host
	c.takes = channel.Takes(cfg)
	return nil
}

func (c *component) GetChannel(context.Context, *notificationpb.GetChannelRequest) (*notificationpb.GetChannelResponse, error) {
	return &notificationpb.GetChannelResponse{Title: "ntfy", Destination: c.host + "/" + c.topic, Categories: c.takes}, nil
}

// Health is down while the last delivery failed.
func (c *component) Health(context.Context) sdk.Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErr != nil {
		return sdk.Health{State: sdk.Down, Message: "last delivery failed: " + c.lastErr.Error()}
	}
	return sdk.Health{State: sdk.OK}
}

func (c *component) Deliver(ctx context.Context, req *notificationpb.DeliverRequest) (*notificationpb.DeliverResponse, error) {
	id, err := c.publish(ctx, req.GetNotification())
	if err != nil {
		return nil, err
	}
	return &notificationpb.DeliverResponse{Id: id}, nil
}

func (c *component) SendTest(ctx context.Context, _ *notificationpb.SendTestRequest) (*notificationpb.SendTestResponse, error) {
	id, err := c.publish(ctx, &notificationpb.Notification{
		Title:    "ddash test",
		Message:  "Notifications from ddash reach this device.",
		Priority: notificationpb.Priority_PRIORITY_DEFAULT,
		Tags:     []string{"white_check_mark"},
	})
	if err != nil {
		return nil, err
	}
	return &notificationpb.SendTestResponse{Id: id}, nil
}

// message is ntfy's JSON publish body.
type message struct {
	Topic    string   `json:"topic"`
	Title    string   `json:"title,omitempty"`
	Message  string   `json:"message,omitempty"`
	Priority int32    `json:"priority,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Click    string   `json:"click,omitempty"`
}

// publish sends n to the topic and returns ntfy's message id. Its outcome
// becomes the Component's health.
func (c *component) publish(ctx context.Context, n *notificationpb.Notification) (string, error) {
	id, err := c.post(ctx, message{
		Topic:    c.topic,
		Title:    n.GetTitle(),
		Message:  n.GetMessage(),
		Priority: int32(n.GetPriority()),
		Tags:     n.GetTags(),
		Click:    n.GetClick(),
	})
	c.mu.Lock()
	c.lastErr = err
	c.mu.Unlock()
	if err != nil {
		return "", status.Error(sendCode(err), err.Error())
	}
	return id, nil
}

func (c *component) post(ctx context.Context, m message) (string, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.root, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return "", fmt.Errorf("%s unreachable: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var answer struct {
		ID    string `json:"id"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &answer)
	if resp.StatusCode/100 != 2 {
		why := answer.Error
		if why == "" {
			why = strings.ToLower(http.StatusText(resp.StatusCode))
		}
		return "", &answered{status: resp.StatusCode, text: fmt.Sprintf("ntfy answered %d: %s", resp.StatusCode, why)}
	}
	return answer.ID, nil
}

// answered is a refusal ntfy answered with, and its HTTP status.
type answered struct {
	status int
	text   string
}

func (a *answered) Error() string { return a.text }

// sendCode is a failed send's gRPC code: PermissionDenied when ntfy
// refused the credentials, as in every -api Component, else Unavailable.
func sendCode(err error) codes.Code {
	var a *answered
	if errors.As(err, &a) && (a.status == http.StatusUnauthorized || a.status == http.StatusForbidden) {
		return codes.PermissionDenied
	}
	return codes.Unavailable
}
