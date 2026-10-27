package router

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/itsKontra/ddash/core/runtime"
)

// StreamJSON is the content type of Connect server-streaming JSON, asked
// for and answered.
const StreamJSON = "application/connect+json"

// A Connect envelope: a header of one byte of flags and a big-endian
// uint32 length, then the message.
const (
	// EnvelopeHeader is the header's length in bytes.
	EnvelopeHeader = 5
	// FlagMessage flags an envelope carrying a message.
	FlagMessage = 0
	// FlagEndStream flags the end-of-stream envelope.
	FlagEndStream = 2
)

// stream answers a Connect server-streaming call: HTTP 200 at once, one
// envelope per message, then one end-of-stream envelope, each flushed so
// nothing between the host and the browser holds it back. Once a write
// fails the browser is gone and nothing more is written.
type stream struct {
	w    http.ResponseWriter
	rc   *http.ResponseController
	gone bool
	// err is a message that couldn't be encoded: no more are sent, and the
	// stream ends with it.
	err error
}

// newStream starts the answer to a streamed call.
func newStream(w http.ResponseWriter) *stream {
	w.Header().Set("Content-Type", StreamJSON)
	// Nginx Proxy Manager buffers responses unless told not to.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	s := &stream{w: w, rc: http.NewResponseController(w)}
	s.flush()
	return s
}

// send sends msg in a message envelope, encoded with opts.
func (s *stream) send(opts protojson.MarshalOptions, msg proto.Message) {
	if s.err != nil {
		return
	}
	b, err := opts.Marshal(msg)
	if err != nil {
		s.err = status.Errorf(codes.Internal, "encode %s: %v", msg.ProtoReflect().Descriptor().FullName(), err)
		return
	}
	s.write(FlagMessage, b)
}

// end ends the stream: {} after a clean end, else {"error": …} with err,
// or a message that couldn't be encoded, as a unary call carries it.
func (s *stream) end(err error) {
	var end struct {
		Error *wireError `json:"error,omitempty"`
	}
	if err = cmp.Or(err, s.err); err != nil {
		_, e := connectError(err)
		end.Error = &e
	}
	b, _ := json.Marshal(end)
	s.write(FlagEndStream, b)
}

// write writes msg in an envelope with flags and flushes it, unless the
// browser is gone; a failed write means it is.
func (s *stream) write(flags byte, msg []byte) {
	if s.gone {
		return
	}
	env := binary.BigEndian.AppendUint32([]byte{flags}, uint32(len(msg)))
	if _, err := s.w.Write(append(env, msg...)); err != nil {
		s.gone = true
		return
	}
	s.flush()
}

// flush sends what was written on to the browser; a failed flush means it
// is gone.
func (s *stream) flush() {
	if err := s.rc.Flush(); err != nil {
		s.gone = true
	}
}

// readEnvelope returns the message in body, which must be exactly one
// uncompressed envelope.
func readEnvelope(body []byte) ([]byte, error) {
	if len(body) < EnvelopeHeader {
		return nil, errors.New("want one enveloped message")
	}
	if body[0] != FlagMessage {
		return nil, fmt.Errorf("envelope flags %#x: compression isn't supported", body[0])
	}
	if n := binary.BigEndian.Uint32(body[1:EnvelopeHeader]); int64(n) != int64(len(body)-EnvelopeHeader) {
		return nil, fmt.Errorf("envelope of %d bytes in %d: want exactly one enveloped message", n, len(body)-EnvelopeHeader)
	}
	return body[EnvelopeHeader:], nil
}

// relay calls md, a server-streaming method on ep, with req and sends each
// message it answers on to s. It keeps receiving until the Component's
// stream ends, even once the browser is gone, and returns how it ended.
func relay(ctx context.Context, s *stream, ep runtime.Endpoint, md protoreflect.MethodDescriptor, req proto.Message) error {
	cs, err := ep.Conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, FullMethod(ep, md))
	if err != nil {
		return err
	}
	// io.EOF means the stream already ended: RecvMsg says how.
	if err := cs.SendMsg(req); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if err := cs.CloseSend(); err != nil {
		return err
	}
	opts := protojson.MarshalOptions{Resolver: ep.Types}
	for {
		msg := dynamicpb.NewMessage(md.Output())
		if err := cs.RecvMsg(msg); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		s.send(opts, msg)
	}
}
