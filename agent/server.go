package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ServerOptions configure an Agent's server.
type ServerOptions struct {
	Kind    Kind
	Release string
	// StateDir keeps the Agent's certificate and key and, once paired, the
	// token's hash. It is created 0700.
	StateDir string
	// EnrolmentCode is the one-time code ddash showed the Operator. It is
	// ignored once the Agent is paired.
	EnrolmentCode string
	Log           *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Server is the HTTPS server both Agents run: pairing, the token check
// and the job registry, with each Agent's own operations added by Handle.
type Server struct {
	o    ServerOptions
	cert tls.Certificate
	fp   string
	mux  *http.ServeMux
	jobs *Jobs

	mu       sync.Mutex
	token    []byte // the token's SHA-256; nil while unpaired
	failures []time.Time
}

// state is what StateDir/state.json keeps.
type state struct {
	TokenSHA256 string    `json:"token_sha256"`
	Paired      time.Time `json:"paired"`
}

// Pair attempts: more than maxFailures wrong codes within failureWindow
// refuse every attempt until the window has passed.
const (
	maxFailures   = 10
	failureWindow = 10 * time.Minute
)

// NewServer loads (or on first start creates) the Agent's self-signed
// certificate and its pairing state.
func NewServer(o ServerOptions) (*Server, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if err := os.MkdirAll(o.StateDir, 0o700); err != nil {
		return nil, err
	}
	cert, err := loadOrCreateCert(o.StateDir, string(o.Kind))
	if err != nil {
		return nil, err
	}
	s := &Server{o: o, cert: cert, fp: Fingerprint(cert.Leaf), mux: http.NewServeMux(), jobs: NewJobs(o.Now)}
	if b, err := os.ReadFile(filepath.Join(o.StateDir, "state.json")); err == nil {
		var st state
		if err := json.Unmarshal(b, &st); err != nil {
			return nil, fmt.Errorf("state.json: %w", err)
		}
		if s.token, err = hex.DecodeString(st.TokenSHA256); err != nil || len(s.token) != sha256.Size {
			return nil, errors.New("state.json: bad token hash")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	s.mux.HandleFunc("GET "+PathHello, s.hello)
	s.mux.HandleFunc("POST "+PathPair, s.pair)
	s.Handle("GET "+PathJobs, s.listJobs)
	s.Handle("GET "+PathJobs+"/{id}", s.getJob)
	s.Handle("DELETE "+PathJobs+"/{id}", s.collectJob)
	s.Handle("POST "+PathJobs+"/{id}/cancel", s.cancelJob)
	return s, nil
}

// Fingerprint is the SHA-256 of cert's DER, in lower-case hex: what ddash
// pins.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// Fingerprint is the Agent's certificate's.
func (s *Server) Fingerprint() string { return s.fp }

// Paired reports whether the Agent paired with a ddash.
func (s *Server) Paired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token != nil
}

// Jobs is the Agent's job registry.
func (s *Server) Jobs() *Jobs { return s.jobs }

// Handle adds an operation that needs the token.
func (s *Server) Handle(pattern string, h http.HandlerFunc) {
	s.mux.Handle(pattern, s.authed(h))
}

// Handler is the Agent's whole API, for tests.
func (s *Server) Handler() http.Handler { return s.mux }

// TLSConfig serves the Agent's certificate.
func (s *Server) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{s.cert}}
}

// ListenAndServe serves HTTPS on addr until ctx ends, then lets requests
// in flight finish for up to 10 s. Jobs keep their own goroutines.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.mux,
		TLSConfig:         s.TLSConfig(),
		ReadHeaderTimeout: 10 * time.Second,
		// /hello and /v1/pair answer anyone: no request may trickle in
		// forever, and no idle connection stays open.
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 2 * time.Minute,
		ErrorLog:    slog.NewLogLogger(s.o.Log.Handler(), slog.LevelDebug),
	}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	s.o.Log.Info("serving", "kind", s.o.Kind, "release", s.o.Release, "addr", ln.Addr().String(), "fingerprint", s.fp, "paired", s.Paired())
	if err := srv.ServeTLS(ln, "", ""); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) hello(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, Hello{Kind: s.o.Kind, Release: s.o.Release, Paired: s.Paired()})
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	var req PairRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.o.Now()
	recent := s.failures[:0]
	for _, t := range s.failures {
		if now.Sub(t) < failureWindow {
			recent = append(recent, t)
		}
	}
	s.failures = recent
	switch {
	case s.token != nil:
		WriteError(w, http.StatusConflict, CodePaired, "this Agent is already paired; remove its state to pair it again")
		return
	case len(s.failures) >= maxFailures:
		WriteError(w, http.StatusTooManyRequests, CodeTooMany, "too many wrong enrolment codes; try again in 10 minutes")
		return
	case s.o.EnrolmentCode == "":
		WriteError(w, http.StatusForbidden, CodeBadCode, "this Agent was started without an enrolment code")
		return
	case subtle.ConstantTimeCompare([]byte(NormalizeCode(req.Code)), []byte(NormalizeCode(s.o.EnrolmentCode))) != 1:
		s.failures = append(s.failures, now)
		WriteError(w, http.StatusForbidden, CodeBadCode, "wrong enrolment code")
		return
	case len(req.Token) < 32:
		WriteError(w, http.StatusBadRequest, CodeInvalid, "the token is too short")
		return
	}
	sum := sha256.Sum256([]byte(req.Token))
	b, _ := json.Marshal(state{TokenSHA256: hex.EncodeToString(sum[:]), Paired: now.UTC()})
	if err := WriteFileAtomic(filepath.Join(s.o.StateDir, "state.json"), b, 0o600); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeFailed, "can't keep the pairing: "+err.Error())
		return
	}
	s.token = sum[:]
	s.o.EnrolmentCode = ""
	s.o.Log.Info("paired with ddash")
	WriteJSON(w, http.StatusOK, Hello{Kind: s.o.Kind, Release: s.o.Release, Paired: true})
}

func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		want := s.token
		s.mu.Unlock()
		if want == nil {
			WriteError(w, http.StatusForbidden, CodeUnpaired, "this Agent isn't paired yet")
			return
		}
		token, ok := strings.CutPrefix(r.Header.Get(TokenHeader), "Bearer ")
		sum := sha256.Sum256([]byte(token))
		if !ok || subtle.ConstantTimeCompare(sum[:], want) != 1 {
			WriteError(w, http.StatusUnauthorized, CodeBadToken, "wrong token")
			return
		}
		h(w, r)
	})
}

func (s *Server) listJobs(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, s.jobs.List())
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no job "+r.PathValue("id"))
		return
	}
	WriteJSON(w, http.StatusOK, j)
}

func (s *Server) collectJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.jobs.Collect(r.PathValue("id"))
	switch {
	case errors.Is(err, errNoJob):
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case err != nil:
		WriteError(w, http.StatusConflict, CodeBusy, err.Error())
	default:
		WriteJSON(w, http.StatusOK, j)
	}
}

// cancelJob answers 202 with the job as it was when asked to stop: it
// ends a moment later, read with GET.
func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.jobs.Cancel(r.PathValue("id"))
	switch {
	case errors.Is(err, errNoJob):
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, ErrEnded):
		WriteError(w, http.StatusConflict, CodeEnded, err.Error())
	case errors.Is(err, ErrNotCancellable):
		WriteError(w, http.StatusConflict, CodeNotCancellable, err.Error())
	case err != nil:
		WriteError(w, http.StatusInternalServerError, CodeFailed, err.Error())
	default:
		WriteJSON(w, http.StatusAccepted, JobRef{Job: j})
	}
}

// NewCode returns a fresh enrolment code, e.g. "K7QX-M2PA-9RTD-HF4C":
// 16 characters of an alphabet without look-alikes, 80 bits.
func NewCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	var out strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			out.WriteByte('-')
		}
		out.WriteByte(alphabet[int(c)%len(alphabet)])
	}
	return out.String()
}

// NormalizeCode compares codes without their dashes, spaces and case.
func NormalizeCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

// NewToken returns a fresh token: 32 random bytes in hex.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WriteJSON answers v as JSON with status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError answers an Error.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, Error{Code: code, Message: message})
}

// maxBody bounds a request: a Compose file and its .env fit easily.
const maxBody = 4 << 20

// ReadJSON decodes r's body into v, answering 400 and returning false
// when it can't.
func ReadJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalid, "bad request: "+err.Error())
		return false
	}
	return true
}

func loadOrCreateCert(dir, name string) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
		return cert, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "ddash " + name + " agent on " + host},
		NotBefore:    time.Now().Add(-time.Hour),
		// Pinned, never verified against a CA: its validity doesn't matter.
		NotAfter:    time.Now().AddDate(100, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := WriteFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := WriteFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, err
	}
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return tls.Certificate{}, err
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	return cert, err
}

// WriteFileAtomic writes b to path through a temp file and a rename, so
// a reader sees the old content or the new, never part of it.
func WriteFileAtomic(path string, b []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(perm); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
