package fakelab

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/itsKontra/ddash/agent"
	"github.com/itsKontra/ddash/agent/agentfake"
	"github.com/itsKontra/ddash/components/compose/composepb"
)

// DockerAgent is a fake Docker Agent the lab runs: ddash adds it by name
// and URL (cmd/dev does, through the API) and pairs with it.
type DockerAgent struct {
	Name  string
	URL   string
	Roots []string
	Fake  *agentfake.Docker
}

// The lab's Docker host with an Agent: Compose Projects under one root,
// one image newer in the registry than what runs (jellyfin, sonarr), a
// Protected one (the Reverse Proxy), one built locally, one not started,
// and a container started without Compose.
var dockerSeed = agentfake.DockerSeed{
	Stacks: []agentfake.Stack{
		{Dir: "media", Running: true, Env: "TZ=Europe/Berlin\nPUID=1000\nPGID=1000\n", Compose: `services:
  jellyfin:
    image: jellyfin/jellyfin:10.10.3
    restart: unless-stopped
    ports: ["8096:8096"]
    volumes:
      - ./config:/config
      - /srv/media:/media:ro
    environment:
      TZ: ${TZ}
  sonarr:
    image: linuxserver/sonarr:4.0.11
    restart: unless-stopped
    ports: ["8989:8989"]
    environment:
      PUID: ${PUID}
      PGID: ${PGID}
      TZ: ${TZ}
    volumes:
      - ./sonarr:/config
`},
		{Dir: "paperless", Running: true, Compose: `services:
  broker:
    image: redis:7.4
    restart: unless-stopped
  db:
    image: postgres:16.6
    restart: unless-stopped
    environment:
      POSTGRES_DB: paperless
      POSTGRES_USER: paperless
      POSTGRES_PASSWORD: ${DB_PASSWORD}
    volumes:
      - ./pgdata:/var/lib/postgresql/data
  webserver:
    image: paperlessngx/paperless-ngx:2.13.5
    restart: unless-stopped
    depends_on: [db, broker]
    ports: ["8000:8000"]
    environment:
      PAPERLESS_REDIS: redis://broker:6379
      PAPERLESS_DBHOST: db
`, Env: "DB_PASSWORD=change-me\n"},
		{Dir: "vaultwarden", Running: true, Compose: `services:
  vaultwarden:
    image: vaultwarden/server:1.32.7
    restart: unless-stopped
    ports: ["8081:80"]
    volumes:
      - ./data:/data
`},
		{Dir: "proxy", Running: true, Compose: `services:
  npm:
    image: jc21/nginx-proxy-manager:2.12.1
    restart: unless-stopped
    ports: ["80:80", "443:443", "81:81"]
    volumes:
      - ./data:/data
      - ./letsencrypt:/etc/letsencrypt
    labels:
      # The Reverse Proxy: reaching ddash depends on it.
      ddash.protected: "true"
`},
		{Dir: "dashboard", Running: true, Compose: `services:
  app:
    build: .
    restart: unless-stopped
    ports: ["3001:3000"]
`, Dockerfile: `FROM node:22-alpine
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY . .
EXPOSE 3000
CMD ["node", "server.js"]
`},
		{Dir: "uptime", Compose: `services:
  kuma:
    image: louislam/uptime-kuma:1.23.16
    restart: unless-stopped
    ports: ["3002:3001"]
    volumes:
      - ./data:/app/data
`},
	},
	Loose: []agentfake.Loose{{Name: "portainer_agent", Image: "portainer/agent:2.21.4", Ports: []agent.Port{{PublicPort: 9001, PrivatePort: 9001, Protocol: "tcp"}}}},
	Images: []agentfake.Image{
		{Ref: "jellyfin/jellyfin:10.10.3", ID: "sha256:jf10103", Latest: "sha256:jf10103-rebuilt"},
		{Ref: "linuxserver/sonarr:4.0.11", ID: "sha256:sonarr4011", Latest: "sha256:sonarr4011-ls262"},
		{Ref: "redis:7.4", ID: "sha256:redis74"},
		{Ref: "postgres:16.6", ID: "sha256:pg166"},
		{Ref: "paperlessngx/paperless-ngx:2.13.5", ID: "sha256:paperless2135"},
		{Ref: "vaultwarden/server:1.32.7", ID: "sha256:vw1327"},
		{Ref: "jc21/nginx-proxy-manager:2.12.1", ID: "sha256:npm2121"},
		{Ref: "louislam/uptime-kuma:1.23.16", ID: "sha256:kuma12316"},
		{Ref: "portainer/agent:2.21.4", ID: "sha256:pagent2214"},
	},
}

// dockerAgentPort is where the lab's Docker Agent listens when it can, so
// the URL ddash keeps stays right across runs (its certificate is kept
// under the temp dir too).
const dockerAgentPort = 17443

// stateDir is where the lab's Agents keep their certificate and pairing.
func stateDir(name string) string {
	return filepath.Join(os.TempDir(), "ddash-fakelab", name)
}

// serveAgent serves a fake Agent on host:port, or on a free port when that
// is taken (by another lab, a parallel test): trying the bind itself, as a
// probe-then-bind races with the other labs.
func serveAgent(serve func(addr string) (string, func(), error), host string, port int) (string, func(), error) {
	if u, stop, err := serve(net.JoinHostPort(host, fmt.Sprint(port))); err == nil {
		return u, stop, nil
	}
	return serve(net.JoinHostPort(host, "0"))
}

// startDocker starts the lab's Docker Agent and the registry its images
// come from, and points compose at that registry.
func (l *Lab) startDocker(host string) error {
	root, err := os.MkdirTemp("", "ddash-fakelab-stacks-")
	if err != nil {
		return err
	}
	l.cleanup = append(l.cleanup, func() { os.RemoveAll(root) })
	fake, err := agentfake.NewDocker(root, dockerSeed, agentfake.Options{StateDir: stateDir("docker-apps"), StepDelay: 1500 * time.Millisecond})
	if err != nil {
		return err
	}
	u, stop, err := serveAgent(fake.Serve, host, dockerAgentPort)
	if err != nil {
		return err
	}
	l.cleanup = append(l.cleanup, stop)
	l.DockerAgents = append(l.DockerAgents, DockerAgent{Name: "apps", URL: u, Roots: []string{root}, Fake: fake})

	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: agentfake.NewRegistry(fake.Engine), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln) //nolint:errcheck // ends with Close
	l.servers = append(l.servers, srv)
	registry := "http://" + ln.Addr().String()
	l.config["compose"] = &composepb.ComposeConfig{DockerHubRegistry: &registry}
	return nil
}
