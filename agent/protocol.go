// Package agent is what the two Agents and ddash share: the HTTPS JSON
// protocol an Agent serves, the server plumbing both Agents run on
// (pairing, the pinned self-signed certificate, the token, jobs), and the
// client ddash's Agent API Components reach an Agent with.
//
// An Agent is a ddash program the Operator installs on a host so ddash can
// change it: the Docker Agent (a container with the Docker socket) or the
// Host Agent (a root service). Both run the Release of the ddash that
// serves them, accept only the ddash they paired with, and expose a fixed
// set of named operations, never "run this command".
//
// Pairing: ddash creates a one-time enrolment code and shows it to the
// Operator, who hands it to the Agent at install. ddash then calls
// POST /v1/pair with the code and a token it generated; the Agent keeps the
// token's hash and forgets the code, and ddash pins the fingerprint of the
// Agent's self-signed certificate (trust on first use). Every later
// request carries the token, and ddash refuses an Agent whose fingerprint
// changed.
package agent

import (
	"time"

	containerpb "github.com/itsKontra/ddash/proto/ddash/container"
)

// Release is the Release an Agent binary was built for, set at build time
// with -ldflags "-X github.com/itsKontra/ddash/agent.Release=v1.2.3".
var Release = "dev"

// Kind is which Agent answers.
type Kind string

const (
	KindDocker Kind = "docker"
	KindHost   Kind = "host"
)

// Default ports: the Docker Agent and the Host Agent may share a host.
const (
	DockerPort = 7443
	HostPort   = 7444
)

// Paths of the protocol. Every path but PathPair and PathHello needs the
// token.
const (
	PathHello = "/v1/hello"
	PathPair  = "/v1/pair"
	PathInfo  = "/v1/info"
	PathJobs  = "/v1/jobs"
	// PathJob is PathJobs + "/<id>": GET reads one, DELETE collects it,
	// and POST to it + "/cancel" cancels it (an Agent older than cancel
	// answers that 404 without an Error).
	PathUpdate = "/v1/agent/update"

	// The Docker Agent's.
	PathContainers = "/v1/containers"
	PathProjects   = "/v1/projects"
	PathFile       = "/v1/projects/file"
	PathValidate   = "/v1/projects/validate"
	PathWrite      = "/v1/projects/write"
	PathCreate     = "/v1/projects/create"
	PathCompose    = "/v1/projects/compose"

	// The Host Agent's.
	PathPackages        = "/v1/packages"
	PathPackagesRefresh = "/v1/packages/refresh"
	PathUpgrade         = "/v1/packages/upgrade"
	PathSSH             = "/v1/ssh"
	PathSSHEnable       = "/v1/ssh/enable"
	PathSSHKeys         = "/v1/ssh/keys"
	PathSSHSettings     = "/v1/ssh/settings"
)

// TokenHeader carries the token: "Authorization: Bearer <token>".
const TokenHeader = "Authorization"

// Error is every non-2xx answer's body.
type Error struct {
	// Code is a stable reason: see the Code* constants.
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error codes.
const (
	CodeUnpaired   = "unpaired"
	CodePaired     = "already_paired"
	CodeBadCode    = "bad_enrolment_code"
	CodeBadToken   = "bad_token"
	CodeInvalid    = "invalid"
	CodeNotFound   = "not_found"
	CodeConflict   = "conflict"
	CodeBusy       = "busy"
	CodeFailed     = "failed"
	CodeTooMany    = "too_many_attempts"
	CodeOutOfRoots = "outside_roots"
	// A cancel of a job that already ended, or of one that can't be
	// cancelled (only Compose jobs can).
	CodeEnded          = "ended"
	CodeNotCancellable = "not_cancellable"
)

// Hello is GET /v1/hello: what an Agent says before pairing.
type Hello struct {
	Kind    Kind   `json:"kind"`
	Release string `json:"release"`
	Paired  bool   `json:"paired"`
}

// PairRequest is POST /v1/pair.
type PairRequest struct {
	Code  string `json:"code"`
	Token string `json:"token"`
}

// Info is GET /v1/info.
type Info struct {
	Kind     Kind   `json:"kind"`
	Release  string `json:"release"`
	Hostname string `json:"hostname"`
	Arch     string `json:"arch"`
	// The Docker Agent's: the Compose roots it mounts.
	Roots []string `json:"roots,omitempty"`
	// The Docker Agent's: the Docker Engine's and Compose's versions.
	Docker  string `json:"docker,omitempty"`
	Compose string `json:"compose,omitempty"`
	// The Host Agent's.
	OS *OS `json:"os,omitempty"`
}

// OS is the Host Agent's host, from /etc/os-release.
type OS struct {
	// ID is os-release's ID: ubuntu, debian, alpine, fedora or arch.
	ID      string `json:"id"`
	Version string `json:"version"`
	Pretty  string `json:"pretty"`
	// PackageManager is apt, dnf, apk or pacman.
	PackageManager string `json:"package_manager"`
	// Init is systemd or openrc.
	Init string `json:"init"`
	// RebootRequired: an update asks for a reboot (/var/run/reboot-required,
	// or a running kernel older than the installed one).
	RebootRequired bool `json:"reboot_required"`
}

// UpdateRequest is POST /v1/agent/update: become Release release.
type UpdateRequest struct {
	Release string `json:"release"`
	// The Host Agent's: where its new binary is and its SHA-256 (hex).
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	// The Docker Agent's: the image to recreate itself on.
	Image string `json:"image,omitempty"`
}

// JobState is where a job or a step of one is.
type JobState string

const (
	Pending JobState = "pending"
	Running JobState = "running"
	Done    JobState = "done"
	Failed  JobState = "failed"
	// Skipped: a step that didn't need to run, e.g. recreating when no
	// image changed.
	Skipped JobState = "skipped"
	// Cancelled: ddash cancelled it while it ran, and it stopped.
	Cancelled JobState = "cancelled"
)

// Ended reports whether s is a final state.
func (s JobState) Ended() bool { return s == Done || s == Failed || s == Skipped || s == Cancelled }

// Job is a long operation an Agent runs on its own, so it outlasts the
// call that started it (and ddash's 60 s Action deadline).
type Job struct {
	ID string `json:"id"`
	// Op is the operation, e.g. "compose.update" or "packages.upgrade".
	Op string `json:"op"`
	// Title is what it does, e.g. "Update paperless".
	Title string `json:"title"`
	// Target names what it acts on, e.g. a Compose Project's name.
	Target string    `json:"target,omitempty"`
	State  JobState  `json:"state"`
	Steps  []Step    `json:"steps"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end,omitzero"`
	// Error is why it failed; empty unless State is Failed.
	Error string `json:"error,omitempty"`
	// Result is the operation's own outcome, e.g. which services changed.
	Result map[string]string `json:"result,omitempty"`
}

// Step is one step of a Job.
type Step struct {
	Title  string   `json:"title"`
	State  JobState `json:"state"`
	Detail string   `json:"detail,omitempty"`
	// Log is the step's last output lines.
	Log []string `json:"log,omitempty"`
}

// JobRef is the answer of every call that starts a job.
type JobRef struct {
	Job Job `json:"job"`
}

// --- Docker Agent ---

// Container is one container, as GET /v1/containers lists it.
type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
	// ImageID is the local image's id, "sha256:…".
	ImageID string `json:"image_id"`
	// RepoDigests are the local image's "<repo>@sha256:…" digests: empty
	// for an image built locally or never pulled by digest.
	RepoDigests []string          `json:"repo_digests,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Ports       []Port            `json:"ports,omitempty"`
	Created     time.Time         `json:"created"`
}

// Port is a published port.
type Port struct {
	IP          string `json:"ip,omitempty"`
	PublicPort  uint16 `json:"public_port"`
	PrivatePort uint16 `json:"private_port"`
	Protocol    string `json:"protocol"`
}

// The Compose labels a container carries.
const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelOneOff      = "com.docker.compose.oneoff"
	// LabelProtected marks a container ddash may not stop, restart or
	// recreate without an explicit override.
	LabelProtected = containerpb.LabelProtected
)

// Project is one Compose Project, as GET /v1/projects lists it.
type Project struct {
	Name       string `json:"name"`
	WorkingDir string `json:"working_dir"`
	// ConfigFiles are the Compose files, absolute; the first is the one
	// ddash reads and writes.
	ConfigFiles []string `json:"config_files"`
	// Services are the Compose services the file defines (or, when it
	// can't be read, those running containers name), sorted.
	Services []Service `json:"services"`
	// Found says how it was found: "labels", "scan" or both.
	Found []string `json:"found"`
	// Error is why its file couldn't be read; the Project is still listed.
	Error string `json:"error,omitempty"`
	// Self says it is the Docker Agent's own Compose Project, the one its
	// container belongs to: Update Agent updates it, and its file isn't
	// read, being outside the Compose roots.
	Self bool `json:"self,omitempty"`
}

// Service is a Compose service of a Project.
type Service struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
	// Ports are its published ports, as the file publishes them.
	Ports []string `json:"ports,omitempty"`
	// Containers are the ids of its containers in GET /v1/containers.
	Containers []string `json:"containers,omitempty"`
}

// ProjectsResponse is GET /v1/projects.
type ProjectsResponse struct {
	Projects   []Project   `json:"projects"`
	Containers []Container `json:"containers"`
}

// FileRequest is GET /v1/projects/file?path=<compose file>.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// SHA256 is the content's, hex: a write expects it.
	SHA256 string `json:"sha256"`
	// Env is the Project's .env beside it, when there is one.
	Env *File `json:"env,omitempty"`
	// Dockerfiles are those its Compose services build from (build:),
	// when under a Compose root and there.
	Dockerfiles []Dockerfile `json:"dockerfiles,omitempty"`
}

// Dockerfile is a Dockerfile some Compose services build from.
type Dockerfile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
	// Services are the Compose services built from it.
	Services []string `json:"services"`
}

// ValidateRequest is POST /v1/projects/validate: would content, as path,
// pass `docker compose config`?
type ValidateRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	// Project is the Compose project name to validate as; empty: the
	// directory's.
	Project string `json:"project,omitempty"`
	// Env is a new Project's .env, to validate with as if beside it;
	// empty: the .env on disk, if any.
	Env string `json:"env,omitempty"`
}

// ValidateResponse says whether it passed and, when not, Compose's error.
type ValidateResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// WriteRequest is POST /v1/projects/write: validate content, keep the
// file's current content as <path>.bak and replace it, unless its SHA-256
// is no longer ExpectSHA256 (409 conflict). A .env that isn't there yet is
// created when ExpectSHA256 is empty. A Dockerfile is written only when
// File, its Compose file, builds from it; it isn't validated.
type WriteRequest struct {
	Path         string `json:"path"`
	Content      string `json:"content"`
	ExpectSHA256 string `json:"expect_sha256"`
	Project      string `json:"project,omitempty"`
	// File is the Compose file that builds from the Dockerfile at Path.
	File string `json:"file,omitempty"`
}

// CreateRequest is POST /v1/projects/create: a new Compose Project's
// directory (under a root), its compose.yaml and optionally its .env;
// refused when the file exists. The answer's Env is the .env written.
type CreateRequest struct {
	Name      string `json:"name"`
	Directory string `json:"directory"`
	Content   string `json:"content"`
	// Env is the .env's content, validated with the Compose file and
	// written owner-only; empty for none.
	Env string `json:"env,omitempty"`
}

// ComposeOp is a Compose operation a job runs.
type ComposeOp string

const (
	// Up is `up -d` (of Services, or every service).
	Up ComposeOp = "up"
	// Down is `down` (Services empty) or `stop` of Services.
	Down    ComposeOp = "down"
	Restart ComposeOp = "restart"
	Pull    ComposeOp = "pull"
	// Update is pull, then `up -d` of only the services whose image
	// changed. Its job's Result maps each changed service to
	// "<old digest> <new digest>"; none changed: Result["unchanged"]="true".
	Update ComposeOp = "update"
)

// ComposeRequest is POST /v1/projects/compose: start a job running Op.
type ComposeRequest struct {
	Op ComposeOp `json:"op"`
	// Project is the Compose project's name; WorkingDir and File where
	// its file is.
	Project    string   `json:"project"`
	WorkingDir string   `json:"working_dir"`
	File       string   `json:"file"`
	Services   []string `json:"services,omitempty"`
	// Pull: an Up pulls first (`--pull always`).
	Pull bool `json:"pull,omitempty"`
	// Wait: an Up waits until its containers run (`--wait`, 60 s).
	Wait bool `json:"wait,omitempty"`
	// Build: an Up builds the images first (`--build`).
	Build bool `json:"build,omitempty"`
}

// --- Host Agent ---

// Package is one pending package update.
type Package struct {
	Name    string `json:"name"`
	Current string `json:"current"`
	New     string `json:"new"`
}

// PackagesResponse is GET /v1/packages: the pending updates as last read.
type PackagesResponse struct {
	Packages []Package `json:"packages"`
	// Read is when the package index was last refreshed; zero: never.
	Read time.Time `json:"read,omitzero"`
	// Error is why the last refresh failed.
	Error          string `json:"error,omitempty"`
	RebootRequired bool   `json:"reboot_required"`
}

// SSHState is GET /v1/ssh.
type SSHState struct {
	// Installed: an sshd binary exists.
	Installed bool `json:"installed"`
	Running   bool `json:"running"`
	// Enabled: it starts at boot.
	Enabled bool `json:"enabled"`
	// The effective settings, from `sshd -T`.
	PasswordAuthentication bool   `json:"password_authentication"`
	PermitRootLogin        string `json:"permit_root_login"`
	// Keys are the keys ddash installed (marked "ddash:<name>") and any
	// other key, in every user's authorized_keys the Agent reads (root and
	// users with a login shell).
	Keys []AuthorizedKey `json:"keys"`
	// Users are the users keys can be installed for.
	Users []string `json:"users"`
}

// AuthorizedKey is one line of an authorized_keys file.
type AuthorizedKey struct {
	User string `json:"user"`
	// Name is the ddash SSH Key's name, from its "ddash:<name>" comment;
	// empty for a key ddash didn't install.
	Name        string `json:"name,omitempty"`
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment,omitempty"`
}

// KeyRequest is POST (install) and DELETE (remove) /v1/ssh/keys.
type KeyRequest struct {
	User string `json:"user"`
	Name string `json:"name"`
	// PublicKey is the key's authorized_keys line ("ssh-ed25519 AAAA…");
	// unused by DELETE.
	PublicKey string `json:"public_key,omitempty"`
}

// SSHSettings is POST /v1/ssh/settings, applied through the drop-in
// /etc/ssh/sshd_config.d/50-ddash.conf only after `sshd -t` passed, then a
// reload. Turning password login off is refused unless a key is installed.
type SSHSettings struct {
	PasswordAuthentication bool `json:"password_authentication"`
	// PermitRootLogin is "yes", "prohibit-password" or "no".
	PermitRootLogin string `json:"permit_root_login"`
}
