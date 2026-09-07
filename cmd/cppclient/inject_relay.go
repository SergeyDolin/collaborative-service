package main

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// InjectRelay is a TCP server on 127.0.0.1:injectPort.
// rtkrcv's inpstr4 (tcpcli) connects here; with -port <injectPort> rtkrcv
// parses the stream as a partner TEXT solution: "week tow X Y Z Q NS SDX SDY SDZ".
//
// ForwardFromRTKFile reads a local RTK/MB solution file and forwards positions
// with their real standard deviations from the .sol file — no hardcoded values.
// The first rtkWarmupSkip solutions are discarded while the RTK/MB filter
// converges, after which all Q==1 and Q==2 solutions are relayed verbatim.
type InjectRelay struct {
	addr string

	mu       sync.Mutex
	cppConns []net.Conn
}

func newInjectRelay(port int) *InjectRelay {
	return &InjectRelay{addr: fmt.Sprintf("127.0.0.1:%d", port)}
}

// Serve accepts connections from rtkrcv's inpstr4 until stopCh is closed.
func (r *InjectRelay) Serve(stopCh <-chan struct{}) {
	ln, err := net.Listen("tcp", r.addr)
	if err != nil {
		return
	}
	go func() {
		<-stopCh
		ln.Close()
		r.mu.Lock()
		for _, c := range r.cppConns {
			c.Close()
		}
		r.cppConns = nil
		r.mu.Unlock()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		r.mu.Lock()
		r.cppConns = append(r.cppConns, conn)
		r.mu.Unlock()
		go r.watchDisconnect(conn)
	}
}

// watchDisconnect blocks until rtkrcv closes the inpstr4 connection.
// rtkrcv never sends on inpstr4, so io.Copy drains silently until remote close.
func (r *InjectRelay) watchDisconnect(conn net.Conn) {
	io.Copy(io.Discard, conn) //nolint:errcheck
	r.mu.Lock()
	for i, c := range r.cppConns {
		if c == conn {
			r.cppConns = append(r.cppConns[:i], r.cppConns[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	conn.Close()
}

// broadcast writes data to all connected CPP inpstr4 connections.
func (r *InjectRelay) broadcast(data []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.cppConns {
		c.SetWriteDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
		c.Write(data)                                        //nolint:errcheck
	}
}

// ForwardFromRTKFile monitors a local rtkrcv solution file written by a
// parallel RTK solver (rtk-collab mode) and injects RTK positions with their
// real standard deviations into every connected CPP inpstr4 stream.
//
// The first rtkWarmupSkip accepted solutions are silently discarded: the RTK/MB
// filter itself is still converging at startup, so its early positions carry
// large, unknown errors that are not reflected in the reported SDs yet.
// After the warmup, both Q==1 (fix) and Q==2 (float) solutions are forwarded
// with their actual SDs from the .sol file — no hardcoded values.
//
// The CPP Kalman filter uses the SDs directly as initial position variance
// (initx), so the filter knows exactly how much to trust the injected position.
//
// On exit (candidate disconnect), a zero-position packet is broadcast so that
// ppp.c clears RTK_sol, triggers cold→warm in 5 epochs, and continues from
// its current good filter state without regression.
const rtkWarmupSkip = 30 // skip first N RTK/MB solutions while their filter converges (~30 s)

func (r *InjectRelay) ForwardFromRTKFile(solFile string, stopCh <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// On exit: zero RTK_sol in ppp.c so cold-phase stops injecting stale data.
	// CPP will transition cold→warm and converge from its current good state.
	defer r.broadcast([]byte("0 0.000 0.0000 0.0000 0.0000 0 0 0.0000 0.0000 0.0000\n"))

	var lastLine string
	var lastFix [3]float64
	var lastQ, lastNs int
	var lastSDX, lastSDY, lastSDZ float64
	seenCount := 0  // total parsed Q==1/Q==2 solutions (including warmup)
	acceptCount := 0 // solutions forwarded to CPP (after warmup)

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
		}

		line, ok := lastSolutionLine(solFile)
		if !ok || line == lastLine {
			// No new solution — re-broadcast last accepted position with a
			// fresh GPS tow so rtkrcv's watchdog timer doesn't expire.
			if acceptCount > 0 {
				week, tow := gpsWeekTow()
				text := fmt.Sprintf("%d %.3f %.4f %.4f %.4f %d %d %.4f %.4f %.4f\n",
					week, tow, lastFix[0], lastFix[1], lastFix[2],
					lastQ, lastNs, lastSDX, lastSDY, lastSDZ)
				r.broadcast([]byte(text))
			}
			continue
		}
		lastLine = line

		x, y, z, q, ns, sdx, sdy, sdz, ok2 := parseSolutionLine(line)
		if !ok2 {
			continue
		}
		// Accept Q==1 (fix) and Q==2 (float); discard single (Q==5) etc.
		if q != 1 && q != 2 {
			continue
		}

		seenCount++
		if seenCount <= rtkWarmupSkip {
			// RTK/MB filter is still converging — skip this solution.
			continue
		}

		lastFix = [3]float64{x, y, z}
		lastQ, lastNs = q, ns
		lastSDX, lastSDY, lastSDZ = sdx, sdy, sdz
		acceptCount++

		week, tow := gpsWeekTow()
		text := fmt.Sprintf("%d %.3f %.4f %.4f %.4f %d %d %.4f %.4f %.4f\n",
			week, tow, lastFix[0], lastFix[1], lastFix[2],
			lastQ, lastNs, lastSDX, lastSDY, lastSDZ)
		r.broadcast([]byte(text))
	}
}

// ----------------------------------------------------------------
// GPS time
// ----------------------------------------------------------------

// gpsWeekTow returns the current GPS week number and time-of-week (seconds).
func gpsWeekTow() (week int, tow float64) {
	const (
		gpsEpochUnix = 315964800 // Unix: 1980-01-06 00:00:00 UTC
		leapSeconds  = 18        // GPS−UTC offset (as of 2017, valid through 2026+)
		weekSeconds  = 7 * 24 * 3600
	)
	now := time.Now().Unix() + leapSeconds
	elapsed := now - gpsEpochUnix
	week = int(elapsed / weekSeconds)
	tow = float64(elapsed % weekSeconds)
	return
}

