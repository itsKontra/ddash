package dockeragent

import (
	"context"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/itsKontra/ddash/agent"
)

// Engine is the Docker Engine behind the mounted socket.
type Engine struct {
	c *client.Client
}

// NewEngine connects to the Engine at host ("unix:///var/run/docker.sock"),
// negotiating the API version.
func NewEngine(host string) (*Engine, error) {
	c, err := client.New(client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Engine{c: c}, nil
}

func (e *Engine) Version(ctx context.Context) (string, error) {
	v, err := e.c.ServerVersion(ctx, client.ServerVersionOptions{})
	return v.Version, err
}

func (e *Engine) Containers(ctx context.Context) ([]agent.Container, error) {
	res, err := e.c.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}
	digests := map[string][]string{} // image id → repo digests
	out := []agent.Container{}
	for _, s := range res.Items {
		c := agent.Container{
			ID:      s.ID,
			Image:   s.Image,
			ImageID: s.ImageID,
			State:   string(s.State),
			Status:  s.Status,
			Labels:  s.Labels,
			Created: time.Unix(s.Created, 0).UTC(),
		}
		if len(s.Names) > 0 {
			c.Name = strings.TrimPrefix(s.Names[0], "/")
		}
		for _, p := range s.Ports {
			if p.PublicPort == 0 {
				continue
			}
			port := agent.Port{PublicPort: p.PublicPort, PrivatePort: p.PrivatePort, Protocol: p.Type}
			if p.IP.IsValid() {
				port.IP = p.IP.String()
			}
			c.Ports = append(c.Ports, port)
		}
		d, ok := digests[s.ImageID]
		if !ok {
			if img, err := e.c.ImageInspect(ctx, s.ImageID); err == nil {
				d = img.RepoDigests
			}
			digests[s.ImageID] = d
		}
		c.RepoDigests = d
		out = append(out, c)
	}
	return out, nil
}

func (e *Engine) ImageDigests(ctx context.Context, ref string) (string, []string, error) {
	img, err := e.c.ImageInspect(ctx, ref)
	if err != nil {
		return "", nil, err
	}
	return img.ID, img.RepoDigests, nil
}
