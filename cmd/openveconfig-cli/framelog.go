package main

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/bolchisb/openveconfig/pkg/mk2"
)

// frameLog wraps a transport and writes every frame that passes through it
// to w, one line per frame:
//
//	15:04:05.000 TX 02 FF 56 A9
//	15:04:05.012 RX 07 FF 56 93 3E 11 00 42 76
//	15:04:05.020 -- baud 115200
//
// TX lines show each write as it was sent (a frame, or a sync string such
// as 55 55 55 55 55). Received bytes are split into frames with
// mk2.ScanFrames; bytes that do not form a frame are logged as "RX ??".
type frameLog struct {
	t transport

	mu     sync.Mutex
	w      io.Writer
	rx     []byte // received bytes not yet logged
	closed bool   // nothing is logged after Close
	now    func() time.Time
}

func newFrameLog(t transport, w io.Writer) *frameLog {
	return &frameLog{t: t, w: w, now: time.Now}
}

func (l *frameLog) Write(b []byte) (int, error) {
	n, err := l.t.Write(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	if n > 0 {
		l.line("TX", b[:n])
	}
	if err != nil {
		l.logf("TX error: %v", err)
	}
	return n, err
}

func (l *frameLog) Read(b []byte) (int, error) {
	n, err := l.t.Read(b)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rx = append(l.rx, b[:n]...)
	var junk []byte
	for len(l.rx) > 0 {
		adv, tok, _ := mk2.ScanFrames(l.rx, false)
		if adv == 0 {
			break
		}
		if tok == nil {
			junk = append(junk, l.rx[:adv]...)
		} else {
			l.line("RX ??", junk)
			junk = nil
			l.line("RX", tok)
		}
		l.rx = l.rx[adv:]
	}
	l.line("RX ??", junk)
	if err != nil && err != io.EOF {
		l.logf("RX error: %v", err)
	}
	return n, err
}

// SetBaudRate logs the change. Bytes still buffered were received at the
// old rate, so they are logged as they are.
func (l *frameLog) SetBaudRate(baud int) error {
	err := l.t.SetBaudRate(baud)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.line("RX ??", l.rx)
	l.rx = nil
	if err != nil {
		l.logf("-- baud %d: %v", baud, err)
	} else {
		l.logf("-- baud %d", baud)
	}
	return err
}

// Close closes the transport. Bytes still buffered are logged as they are.
func (l *frameLog) Close() error {
	l.mu.Lock()
	l.line("RX ??", l.rx)
	l.rx = nil
	l.closed = true
	l.mu.Unlock()
	return l.t.Close()
}

// line logs b in hex after prefix; it does nothing for an empty b. l.mu
// must be held.
func (l *frameLog) line(prefix string, b []byte) {
	if len(b) == 0 {
		return
	}
	l.logf("%s % X", prefix, b)
}

// logf writes one timestamped line. l.mu must be held.
func (l *frameLog) logf(format string, a ...any) {
	if l.closed {
		return
	}
	msg := strings.TrimRight(fmt.Sprintf(format, a...), "\n")
	fmt.Fprintf(l.w, "%s %s\n", l.now().Format("15:04:05.000"), msg)
}
