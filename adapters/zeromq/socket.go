package zeromq

import (
	"errors"
	"time"
)

// ErrTimeout is returned by [FramedSocket.RecvFrames] when no message
// arrived within the socket's configured receive timeout window.
// The adapter receive loops treat ErrTimeout as a non-fatal signal to
// re-check context cancellation and retry.
var ErrTimeout = errors.New("zeromq: receive timeout")

// FramedSocket is the transport interface used by this adapter.
// It abstracts a ZMQ socket down to the minimal surface needed for
// multi-frame send/receive and subscription management.
//
// Wire it to your preferred ZMQ library. Example with pebbe/zmq4:
//
//	import zmq "github.com/pebbe/zmq4"
//
//	type pebbeSocket struct{ s *zmq.Socket }
//
//	func (p *pebbeSocket) SendFrames(frames [][]byte) error {
//	    for i, f := range frames {
//	        flag := zmq.SNDMORE
//	        if i == len(frames)-1 {
//	            flag = 0
//	        }
//	        if _, err := p.s.SendBytes(f, flag); err != nil {
//	            return err
//	        }
//	    }
//	    return nil
//	}
//
//	func (p *pebbeSocket) RecvFrames() ([][]byte, error) {
//	    frames, err := p.s.RecvMessageBytes(0)
//	    if err != nil {
//	        if zmq.AsErrno(err) == zmq.EAGAIN {
//	            return nil, zeromq.ErrTimeout
//	        }
//	        return nil, err
//	    }
//	    return frames, nil
//	}
//
//	func (p *pebbeSocket) SetSubscription(topic string) error {
//	    return p.s.SetSubscribe(topic)
//	}
//
//	func (p *pebbeSocket) SetRecvTimeout(d time.Duration) error {
//	    return p.s.SetRcvtimeo(d)
//	}
type FramedSocket interface {
	// SendFrames sends a multi-frame ZMQ message. Each element of frames is
	// one frame; all but the last are sent with the SNDMORE flag.
	SendFrames(frames [][]byte) error

	// RecvFrames receives the next multi-frame ZMQ message.
	// Returns (nil, [ErrTimeout]) when no message arrives within the
	// socket's configured receive timeout (set via [SetRecvTimeout]).
	// Returns (nil, err) on socket errors.
	RecvFrames() ([][]byte, error)

	// SetSubscription registers a topic prefix filter on a SUB socket.
	// The ZMQ broker delivers only messages whose first frame starts with topic.
	// Call with an empty string to receive all messages on a PULL socket.
	// Calling on non-SUB sockets that ignore the option should return nil.
	SetSubscription(topic string) error

	// SetRecvTimeout configures how long [RecvFrames] blocks before returning
	// [ErrTimeout]. Set to a short interval (e.g. 100 ms) so that receive loops
	// can check context cancellation periodically.
	SetRecvTimeout(d time.Duration) error
}
