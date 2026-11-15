package proxmox

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/itsKontra/ddash/components/host-agent-api/hostagentapipb"
	"github.com/itsKontra/ddash/components/proxmox/proxmoxpb"
	"github.com/itsKontra/ddash/sdk"
	"github.com/itsKontra/ddash/sdk/migrate"
)

// keyName is an SSH Key's name: it ends up as the "ddash:<name>" comment
// on the Workloads it is installed on.
var keyName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

const badKeyName = "a name is letters, digits, ., _ and -, like laptop"

// sdkField is one field's InvalidArgument.
func sdkField(path, message string) error {
	var errs sdk.FieldErrors
	errs.Add(path, "%s", message)
	return errs.Err()
}

func (c *component) ListSSHKeys(ctx context.Context, _ *proxmoxpb.ListSSHKeysRequest) (*proxmoxpb.ListSSHKeysResponse, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT name, public_key, type, fingerprint, created, generated FROM ssh_key ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &proxmoxpb.ListSSHKeysResponse{}
	for rows.Next() {
		k := &proxmoxpb.SSHKey{}
		var created string
		if err := rows.Scan(&k.Name, &k.PublicKey, &k.Type, &k.Fingerprint, &created, &k.Generated); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, created); err == nil {
			k.Created = timestamppb.New(t)
		}
		out.Keys = append(out.Keys, k)
	}
	return out, rows.Err()
}

// store keeps pub under name.
func (c *component) store(ctx context.Context, name string, pub ssh.PublicKey, comment string, generated bool) (*proxmoxpb.SSHKey, error) {
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	if comment = strings.TrimSpace(comment); comment != "" {
		line += " " + comment
	}
	k := &proxmoxpb.SSHKey{
		Name: name, PublicKey: line, Type: pub.Type(), Fingerprint: ssh.FingerprintSHA256(pub),
		Created: timestamppb.Now(), Generated: generated,
	}
	var taken string
	if err := c.db.QueryRowContext(ctx, `SELECT name FROM ssh_key WHERE fingerprint = ?`, k.GetFingerprint()).Scan(&taken); err == nil {
		return nil, status.Errorf(codes.AlreadyExists, "this key is already stored as %s", taken)
	}
	_, err := c.db.ExecContext(ctx, `INSERT INTO ssh_key (name, public_key, type, fingerprint, created, generated) VALUES (?, ?, ?, ?, ?, ?)`,
		k.GetName(), k.GetPublicKey(), k.GetType(), k.GetFingerprint(), k.GetCreated().AsTime().UTC().Format(time.RFC3339), generated)
	if err != nil {
		if migrate.IsTaken(err) {
			return nil, status.Errorf(codes.AlreadyExists, "an SSH Key is already named %s", name)
		}
		return nil, err
	}
	return k, nil
}

// GenerateSSHKey creates the pair in memory: the private key goes into
// the response only, never to the DB or a log.
func (c *component) GenerateSSHKey(ctx context.Context, req *proxmoxpb.GenerateSSHKeyRequest) (*proxmoxpb.GenerateSSHKeyResponse, error) {
	if !keyName.MatchString(req.GetName()) {
		return nil, sdkField("name", badKeyName)
	}
	pubRaw, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	pub, err := ssh.NewPublicKey(pubRaw)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, req.GetName())
	if err != nil {
		return nil, err
	}
	k, err := c.store(ctx, req.GetName(), pub, req.GetName(), true)
	if err != nil {
		return nil, err
	}
	return &proxmoxpb.GenerateSSHKeyResponse{Key: k, PrivateKey: string(pem.EncodeToMemory(block))}, nil
}

func (c *component) AddSSHKey(ctx context.Context, req *proxmoxpb.AddSSHKeyRequest) (*proxmoxpb.AddSSHKeyResponse, error) {
	if !keyName.MatchString(req.GetName()) {
		return nil, sdkField("name", badKeyName)
	}
	text := strings.TrimSpace(req.GetPublicKey())
	if strings.Contains(text, "PRIVATE KEY") {
		return nil, sdkField("public_key", "this is a private key: paste the public one (the .pub file) and keep this one to yourself")
	}
	pub, comment, _, rest, err := ssh.ParseAuthorizedKey([]byte(text))
	if err != nil || strings.TrimSpace(string(rest)) != "" {
		return nil, sdkField("public_key", "must be one public key line, like ssh-ed25519 AAAA… laptop")
	}
	k, err := c.store(ctx, req.GetName(), pub, comment, false)
	if err != nil {
		return nil, err
	}
	return &proxmoxpb.AddSSHKeyResponse{Key: k}, nil
}

func (c *component) DeleteSSHKey(ctx context.Context, req *proxmoxpb.DeleteSSHKeyRequest) (*proxmoxpb.DeleteSSHKeyResponse, error) {
	res, err := c.db.ExecContext(ctx, `DELETE FROM ssh_key WHERE name = ?`, req.GetName())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, status.Errorf(codes.NotFound, "no SSH Key %q", req.GetName())
	}
	return &proxmoxpb.DeleteSSHKeyResponse{}, nil
}

func (c *component) InstallSSHKey(ctx context.Context, req *proxmoxpb.InstallSSHKeyRequest) (*proxmoxpb.InstallSSHKeyResponse, error) {
	w, err := c.find(ctx, req.GetWorkload())
	if err != nil {
		return nil, err
	}
	if len(req.GetNames()) == 0 {
		return nil, sdkField("names", "pick at least one SSH Key")
	}
	if req.GetUser() == "" {
		return nil, sdkField("user", "pick the user to install them for")
	}
	all, err := c.ListSSHKeys(ctx, &proxmoxpb.ListSSHKeysRequest{})
	if err != nil {
		return nil, err
	}
	keys := map[string]*proxmoxpb.SSHKey{}
	for _, k := range all.GetKeys() {
		keys[k.GetName()] = k
	}
	out := &proxmoxpb.InstallSSHKeyResponse{}
	for _, name := range req.GetNames() {
		k := keys[name]
		if k == nil {
			return nil, status.Errorf(codes.NotFound, "no SSH Key %q", name)
		}
		_, err := c.agents.InstallKey(ctx, &hostagentapipb.InstallKeyRequest{Vmid: w.GetVmid(), User: req.GetUser(), Name: name, PublicKey: k.GetPublicKey()})
		switch {
		case status.Code(err) == codes.AlreadyExists:
			out.Already = append(out.Already, name)
		case err != nil:
			return nil, err
		}
	}
	return out, nil
}

// EnableSSH starts the Host Agent's job and collects it in the
// background, in this call's chain.
func (c *component) EnableSSH(ctx context.Context, req *proxmoxpb.EnableSSHRequest) (*proxmoxpb.EnableSSHResponse, error) {
	w, err := c.find(ctx, req.GetWorkload())
	if err != nil {
		return nil, err
	}
	res, err := c.agents.EnableSSH(ctx, &hostagentapipb.EnableSSHRequest{Vmid: w.GetVmid()})
	if err != nil {
		return nil, err
	}
	bg, done := c.background(ctx, FollowAtMost)
	go func() {
		defer done()
		if _, err := c.followJob(bg, w.GetVmid(), res.GetJob().GetId(), nil); err != nil {
			c.send(bg, false, w, "Enabling sshd on "+w.label()+" failed", err.Error())
		}
	}()
	return &proxmoxpb.EnableSSHResponse{Job: res.GetJob()}, nil
}
