package nbio

import (
	"errors"
	"io"
	"time"
)

var ErrNoData = errors.New("no data available at the moment")

type reader struct {
	r     io.ReadCloser // source
	timer *time.Timer   // lazy init, reusable

	// maximum amount of time to wait for data
	timeout time.Duration

	buf []byte // current buffer
	i   int    // position in current buffer

	next chan []byte // following buffer
	pool chan []byte // buffer recycling
	err  chan error  // sticky error store

	// 3 read buffers cycle through next and pool
	buf1, buf2, buf3 [2048]byte
}

// NewReader returns a new non blocking wrapper whose Read function
// gives a time out (with ErrNoData) when applicable.
//
// Errors of the underlying reader are sticky. Once Read returns an
// error other than ErrNoData then all successisive calls will fail
// with the same. The return is either a successful read with n > 0
// or an error and never both. Because of the error persistence the
// implementation stops reading from source on the first error thus
// it is safe to create a new reader for recoverable situations.
func NewReader(source io.ReadCloser, timeout time.Duration) io.ReadCloser {
	r := &reader{
		r:       source,
		timeout: timeout,
		next:    make(chan []byte, 1),
		pool:    make(chan []byte, 2),
		err:     make(chan error, 1),
	}
	r.buf = r.buf1[:0]
	r.pool <- r.buf3[:]

	// read pool and feed next until source error
	go func() {
		buf := r.buf2[:]

		for {
			n, err := r.r.Read(buf)
			if n != 0 {
				r.next <- buf[:n]
				buf = <-r.pool
				buf = buf[:cap(buf)]
			}
			if err != nil {
				r.err <- err
				close(r.next)
				return
			}
		}
	}()

	return r
}

func (r *reader) Close() error {
	err := r.r.Close()

	// flush to kill Go routine
	for buf := range r.next {
		r.pool <- buf
	}

	return err
}

func (r *reader) Read(p []byte) (int, error) {
	if r.timer == nil {
		r.timer = time.NewTimer(r.timeout)
	} else {
		r.timer.Reset(r.timeout)
	}

	// ensure data or timeout
	buf := r.buf
	for buf != nil && r.i >= len(buf) {
		select {
		case <-r.timer.C:
			return 0, ErrNoData

		case buf = <-r.next:
			r.pool <- r.buf
			r.buf = buf
			r.i = 0
		}
	}

	if !r.timer.Stop() {
		<-r.timer.C
	}

	if buf == nil {
		// an error occured
		err := <-r.err
		r.err <- err
		return 0, err
	}

	var n int
	for {
		did := copy(p, buf[r.i:])
		r.i += did
		n += did

		if n >= len(p) {
			// filled buffer
			return n, nil
		}
		p = p[did:]

		select {
		default:
			// don't wait for more
			return n, nil

		case buf = <-r.next:
			r.pool <- r.buf
			r.buf = buf
			r.i = 0

			if buf == nil {
				// an error occured
				return n, nil
			}
		}
	}
}
