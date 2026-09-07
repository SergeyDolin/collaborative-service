package main

import (
	"bufio"
	"fmt"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SolutionServer reads rtkrcv's solution.pos as it grows and broadcasts
// portTCP-format text lines to every connected client:
//
//	"week tow X Y Z Q ns SDX SDY SDZ\n"
//
// X/Y/Z are the candidate's CPP ECEF coordinates (converted from LLH in the
// solution file).  SDX/SDY/SDZ are computed by rotating the NEU standard
// deviations (sdn/sde/sdu from the solution file) into the ECEF frame, so
// rtkrcv's Kalman filter receives accurate uncertainty information instead of
// the previous hard-coded 1000 m values.
//
// Privacy: the coordinates served are the CPP-derived position (same as what
// is embedded in RTCM 1005 by RTCMProxy) — the receiver's true GNSS position
// is never exposed.
type SolutionServer struct {
	addr string

	fileMu  sync.RWMutex
	solFile string

	connMu sync.Mutex
	conns  []net.Conn
}

func newSolutionServer(port int) *SolutionServer {
	return &SolutionServer{addr: fmt.Sprintf(":%d", port)}
}

// SetSolutionFile sets the path to the rtkrcv solution.pos file to tail.
// May be called before or after Serve.
func (s *SolutionServer) SetSolutionFile(path string) {
	s.fileMu.Lock()
	s.solFile = path
	s.fileMu.Unlock()
}

// Serve accepts incoming connections and runs the tail-broadcast loop until
// stopCh is closed.
func (s *SolutionServer) Serve(stopCh <-chan struct{}) {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return
	}
	go func() {
		<-stopCh
		ln.Close()
		s.connMu.Lock()
		for _, c := range s.conns {
			c.Close()
		}
		s.conns = nil
		s.connMu.Unlock()
	}()
	go s.tailLoop(stopCh)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		s.connMu.Lock()
		s.conns = append(s.conns, conn)
		s.connMu.Unlock()
		go s.drainConn(conn)
	}
}

// drainConn removes conn from the active list when the remote side closes it.
// rtkrcv never writes on the portTCP connection, so reads just block until EOF.
func (s *SolutionServer) drainConn(conn net.Conn) {
	buf := make([]byte, 1)
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
		if _, err := conn.Read(buf); err != nil {
			break
		}
	}
	s.connMu.Lock()
	for i, c := range s.conns {
		if c == conn {
			s.conns = append(s.conns[:i], s.conns[i+1:]...)
			break
		}
	}
	s.connMu.Unlock()
	conn.Close()
}

func (s *SolutionServer) broadcast(data []byte) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	for _, c := range s.conns {
		c.SetWriteDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
		c.Write(data)                                        //nolint:errcheck
	}
}

// tailLoop polls solution.pos every second. When a new valid line appears it
// parses coordinates + NEU standard deviations, converts to ECEF, and
// broadcasts portTCP text.  Between file updates it re-broadcasts the last
// good position with a refreshed GPS tow so rtkrcv's watchdog never fires.
func (s *SolutionServer) tailLoop(stopCh <-chan struct{}) {
	var lastLine string
	var lastX, lastY, lastZ float64
	var lastQ, lastNS int
	var lastSDX, lastSDY, lastSDZ float64
	var hasPos bool

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
		}

		s.fileMu.RLock()
		path := s.solFile
		s.fileMu.RUnlock()

		if path != "" {
			if line, ok := lastSolutionLine(path); ok && line != lastLine {
				lastLine = line
				if x, y, z, q, ns, sdx, sdy, sdz, ok2 := parseSolutionLine(line); ok2 {
					lastX, lastY, lastZ = x, y, z
					lastQ, lastNS = q, ns
					lastSDX, lastSDY, lastSDZ = sdx, sdy, sdz
					hasPos = true
				}
			}
		}

		if hasPos {
			week, tow := gpsWeekTow()
			line := fmt.Sprintf("%d %.3f %.4f %.4f %.4f %d %d %.4f %.4f %.4f\n",
				week, tow, lastX, lastY, lastZ, lastQ, lastNS, lastSDX, lastSDY, lastSDZ)
			s.broadcast([]byte(line))
		}
	}
}

// ----------------------------------------------------------------
// solution.pos parsing helpers
// ----------------------------------------------------------------

// lastSolutionLine returns the most recent non-comment, non-empty line from path.
func lastSolutionLine(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	var last string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "%") || strings.TrimSpace(line) == "" {
			continue
		}
		last = line
	}
	return last, last != ""
}

// parseSolutionLine parses an rtkrcv LLH solution line and returns the ECEF
// position and ECEF standard deviations (rotated from the NEU frame).
//
// solution.pos columns:
//
//	0 date   1 time   2 lat(°)  3 lon(°)  4 h(m)
//	5 Q      6 ns     7 sdn(m)  8 sde(m)  9 sdu(m)
//	10 sdne  11 sdeu  12 sdun   13 age    14 ratio  15 cnvg
func parseSolutionLine(line string) (x, y, z float64, q, ns int, sdx, sdy, sdz float64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 10 {
		return
	}
	lat, e1 := strconv.ParseFloat(fields[2], 64)
	lon, e2 := strconv.ParseFloat(fields[3], 64)
	h, e3 := strconv.ParseFloat(fields[4], 64)
	q, e4 := strconv.Atoi(fields[5])
	ns, e5 := strconv.Atoi(fields[6])
	sdn, e6 := strconv.ParseFloat(fields[7], 64)
	sde, e7 := strconv.ParseFloat(fields[8], 64)
	sdu, e8 := strconv.ParseFloat(fields[9], 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil ||
		e5 != nil || e6 != nil || e7 != nil || e8 != nil || q == 0 {
		return
	}

	// LLH → ECEF
	ecef := llhToECEF(lat, lon, h)
	x, y, z = ecef.X, ecef.Y, ecef.Z

	// Rotate NEU covariance diag(sdn², sde², sdu²) → ECEF frame.
	// For NEU→ECEF rotation R:
	//   R[:,N] = [-sin(lat)cos(lon), -sin(lat)sin(lon),  cos(lat)]
	//   R[:,E] = [-sin(lon),          cos(lon),           0       ]
	//   R[:,U] = [ cos(lat)cos(lon),  cos(lat)sin(lon),  sin(lat)]
	// var(X_ecef) = R[0,N]²·sdn² + R[0,E]²·sde² + R[0,U]²·sdu²
	latR := lat * math.Pi / 180
	lonR := lon * math.Pi / 180
	slat, clat := math.Sin(latR), math.Cos(latR)
	slon, clon := math.Sin(lonR), math.Cos(lonR)

	sdx = math.Sqrt(sq(slat*clon*sdn) + sq(slon*sde) + sq(clat*clon*sdu))
	sdy = math.Sqrt(sq(slat*slon*sdn) + sq(clon*sde) + sq(clat*slon*sdu))
	sdz = math.Sqrt(sq(clat*sdn) + sq(slat*sdu))

	ok = true
	return
}

func sq(v float64) float64 { return v * v }
