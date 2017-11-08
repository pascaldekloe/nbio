package nbio

import (
	"errors"
	"io"
	"io/ioutil"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// Feed is a data sample.
const feed = "Hello World!"

// ReadRoutineStackEl is assumed present in the Go routine stack trace.
const readRoutineStackEl = "NewReader"

// Non blocking Reader must indicate no-data and recover.
func TestReadWithPause(t *testing.T) {
	// test subject with (blocking) pipe attached
	pr, pw := io.Pipe()
	defer pw.Close()
	r := NewReader(pr, 9*time.Millisecond)

	buf := make([]byte, len(feed))

	// write two single bytes; should be read as one
	pw.Write([]byte{feed[0]})
	pw.Write([]byte{feed[1]})
	time.Sleep(9 * time.Millisecond) // little time to process
	if n, err := r.Read(buf); n != 2 || err != nil {
		t.Errorf("slow start: Read = (%d, %v), want (2, <nil>)", n, err)
	} else if got := string(buf[:2]); got != feed[:2] {
		t.Errorf("slow start: got %q, want %q", got, feed[:2])
	}

	// no more data; should block for 9ms
	if n, err := r.Read(buf); n != 0 || err != ErrNoData {
		t.Errorf("pipe empty: Read = (%d, %v), want (0, <ErrNoData>)", n, err)
	}

	// recover
	pw.Write([]byte(feed))
	if n, err := r.Read(buf); n != len(feed) || err != nil {
		t.Errorf("pipe refill: Read = (%d, %v), want (%d, <nil>)", n, err, len(feed))
	} else if got := string(buf); got != feed {
		t.Errorf("pipe refill: got %q, want %q", got, feed)
	}
}

// Non blocking Reader must eliminate blocked read routine on close.
func TestReadBlockAbort(t *testing.T) {
	// test subject with blocking pipe attached
	pr, pw := io.Pipe()
	defer pw.Close()
	r := NewReader(pr, time.Hour)

	// ensure read routine
	time.Sleep(9 * time.Millisecond)
	if dump := stackDump(); !strings.Contains(dump, readRoutineStackEl) {
		t.Fatalf("can't locate read routine element %q in:\n%s", readRoutineStackEl, dump)
	}

	r.Close()
	if dump := stackDump(); strings.Contains(dump, readRoutineStackEl) {
		t.Errorf("read routine element %q still present in:\n%s", readRoutineStackEl, dump)
	}
}

// Non blocking Reader must eliminate read routine with pending data on close.
func TestReadPendingAbort(t *testing.T) {
	// test subject with pipe attached
	pr, pw := io.Pipe()
	defer pw.Close()
	r := NewReader(pr, time.Hour)

	go func() {
		for n := 0; n < 7; n++ {
			pw.Write([]byte(feed))
		}
	}()
	// ensure pending data
	time.Sleep(9 * time.Millisecond)
	if dump := stackDump(); !strings.Contains(dump, readRoutineStackEl) {
		t.Fatalf("can't locate read routine element %q in:\n%s", readRoutineStackEl, dump)
	}

	r.Close()
	if dump := stackDump(); strings.Contains(dump, readRoutineStackEl) {
		t.Errorf("read routine element %q still present in:\n%s", readRoutineStackEl, dump)
	}
}

var errOnClose = errors.New("close error test")

type errCloser struct {
	io.Reader
}

func (errCloser) Close() error {
	return errOnClose
}

// Non blocking Reader must pass close errors.
func TestReaderCloseErr(t *testing.T) {
	r := NewReader(errCloser{iotest.DataErrReader(strings.NewReader(feed))}, time.Hour)
	if got := r.Close(); got != errOnClose {
		t.Errorf("got error %v, want %v", got, errOnClose)
	}
}

// Deal with source which returns both success and error in one io.Reader call.
func TestReadBothNAndErr(t *testing.T) {
	r := NewReader(errCloser{iotest.DataErrReader(strings.NewReader(feed))}, time.Hour)

	// ensure read routine started
	time.Sleep(9 * time.Millisecond)

	got, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal("read error:", err)
	}
	if string(got) != feed {
		t.Errorf("got %q, want %q", got, feed)
	}
}

func stackDump() string {
	buf := make([]byte, 2048)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}
