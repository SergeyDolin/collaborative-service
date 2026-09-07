package main

import (
	"fmt"
	"math"
	"net"
	"sync"
	"time"
)

// ----------------------------------------------------------------
// CRC-24Q (RTCM standard)
// ----------------------------------------------------------------

var crc24Table [256]uint32

func init() {
	const poly = 0x1864CFB
	for i := 0; i < 256; i++ {
		crc := uint32(i) << 16
		for j := 0; j < 8; j++ {
			crc <<= 1
			if crc&0x1000000 != 0 {
				crc ^= poly
			}
		}
		crc24Table[i] = crc
	}
}

func crc24q(data []byte) uint32 {
	crc := uint32(0)
	for _, b := range data {
		crc = ((crc << 8) & 0xFFFFFF) ^ crc24Table[((crc>>16)^uint32(b))&0xFF]
	}
	return crc
}

// ----------------------------------------------------------------
// WGS84 LLH → ECEF
// ----------------------------------------------------------------

type ecefXYZ struct{ X, Y, Z float64 }

func llhToECEF(latDeg, lonDeg, h float64) ecefXYZ {
	const (
		a  = 6378137.0
		e2 = 0.00669437999014
	)
	lat := latDeg * math.Pi / 180
	lon := lonDeg * math.Pi / 180
	sinLat := math.Sin(lat)
	N := a / math.Sqrt(1-e2*sinLat*sinLat)
	cosLat := math.Cos(lat)
	return ecefXYZ{
		X: (N + h) * cosLat * math.Cos(lon),
		Y: (N + h) * cosLat * math.Sin(lon),
		Z: (N*(1-e2) + h) * sinLat,
	}
}

// ----------------------------------------------------------------
// RTCM3 frame parser
// ----------------------------------------------------------------

// nextRTCMFrame extracts one valid RTCM3 frame from buf.
func nextRTCMFrame(buf []byte) (frame, rest []byte, ok bool) {
	for len(buf) >= 3 {
		if buf[0] != 0xD3 {
			buf = buf[1:]
			continue
		}
		length := int(buf[1]&0x03)<<8 | int(buf[2])
		total := 3 + length + 3
		if len(buf) < total {
			return nil, buf, false
		}
		candidate := buf[:total]
		want := crc24q(candidate[:3+length])
		got := uint32(candidate[3+length])<<16 | uint32(candidate[3+length+1])<<8 | uint32(candidate[3+length+2])
		if want != got {
			buf = buf[1:]
			continue
		}
		return candidate, buf[total:], true
	}
	return nil, buf, false
}

func rtcm3MsgType(frame []byte) uint16 {
	if len(frame) < 5 {
		return 0
	}
	return uint16(frame[3])<<4 | uint16(frame[4])>>4
}

// ----------------------------------------------------------------
// RTCM 1005 / 1006 patcher
//
// Payload layout (MSB-first bit numbering within data bytes):
//   bits  0-11 : message type (12 bits)
//   bits 12-23 : reference station ID (12 bits)
//   bits 24-29 : ITRF year (6 bits)
//   bit  30    : GPS indicator
//   bit  31    : GLONASS indicator
//   bit  32    : Galileo indicator
//   bit  33    : reference station indicator
//   bits 34-71 : ARP ECEF-X (38-bit signed, unit = 0.0001 m)
//   bit  72    : single oscillator indicator
//   bit  73    : reserved
//   bits 74-111: ARP ECEF-Y (38-bit signed)
//   bits 112-113: quarter cycle indicator
//   bits 114-151: ARP ECEF-Z (38-bit signed)
//   [1006 only] bits 152-167: antenna height (16-bit unsigned, unit = 0.0001 m)
// ----------------------------------------------------------------

// set38 writes a 38-bit signed integer at bit offset bitOff (MSB-first) in data.
// All three ECEF bit offsets (34, 74, 114) satisfy bitOff%8 == 2, so each
// value occupies exactly 5 bytes: top-6 bits in data[b] (preserving top-2),
// then 4 full bytes.
func set38(data []byte, bitOff int, v int64) {
	u := uint64(v) & 0x3FFFFFFFFF // 38-bit two's complement
	b := bitOff / 8
	s := bitOff % 8               // always 2 for our ECEF fields
	keep := byte(0xFF << (8 - s)) // 0xC0 for s=2
	data[b] = (data[b] & keep) | byte(u>>32)
	data[b+1] = byte(u >> 24)
	data[b+2] = byte(u >> 16)
	data[b+3] = byte(u >> 8)
	data[b+4] = byte(u)
}

// build1005Frame constructs a minimal valid RTCM3 1005 frame from ECEF coordinates.
// Used by RTCMProxy to inject a synthetic base-station position into the output stream
// when the rover receiver doesn't transmit its own 1005/1006 messages.
func build1005Frame(pos ecefXYZ) []byte {
	// 19-byte payload layout (all MSB-first):
	//   bits  0-11 : message type = 1005 = 0x3ED
	//   bits 12-23 : reference station ID = 0
	//   bits 24-29 : ITRF year = 0
	//   bit  30    : GPS  = 1
	//   bit  31    : GLONASS = 1
	//   bit  32    : Galileo = 0
	//   bit  33    : ref-station indicator = 0
	//   bits 34-71 : ECEF-X (38-bit signed, 0.0001 m units)
	//   bit  72    : single-oscillator = 0
	//   bit  73    : reserved = 0
	//   bits 74-111: ECEF-Y (38-bit signed)
	//   bits 112-113: quarter-cycle indicator = 0
	//   bits 114-151: ECEF-Z (38-bit signed)
	d := make([]byte, 19)
	d[0] = 0x3E // bits 0-7:  msg type 0-7 = 00111110
	d[1] = 0xD0 // bits 8-15: msg type 8-11=1101, station ID 12-15=0000
	//d[2] = 0x00: station ID 16-23 = 0
	d[3] = 0x03 // bits 24-31: ITRF=0, GPS=1, GLONASS=1 → 00000011

	set38(d, 34, int64(math.Round(pos.X*1e4)))
	set38(d, 74, int64(math.Round(pos.Y*1e4)))
	set38(d, 114, int64(math.Round(pos.Z*1e4)))

	frame := make([]byte, 3+19+3)
	frame[0] = 0xD3 // RTCM3 preamble
	frame[1] = 0x00 // reserved(6) + length_high(2) = 0  (19 < 256)
	frame[2] = 19   // payload length
	copy(frame[3:], d)
	crc := crc24q(frame[:22])
	frame[22] = byte(crc >> 16)
	frame[23] = byte(crc >> 8)
	frame[24] = byte(crc)
	return frame
}

// patchBasePos replaces ECEF-X/Y/Z in a 1005 or 1006 frame.
func patchBasePos(frame []byte, pos ecefXYZ) []byte {
	payloadLen := len(frame) - 6 // frame = 3(hdr) + payload + 3(CRC)
	if payloadLen != 19 && payloadLen != 21 {
		return frame
	}
	out := make([]byte, len(frame))
	copy(out, frame)
	d := out[3:]

	set38(d, 34, int64(math.Round(pos.X*1e4)))
	set38(d, 74, int64(math.Round(pos.Y*1e4)))
	set38(d, 114, int64(math.Round(pos.Z*1e4)))

	crc := crc24q(out[:3+payloadLen])
	out[3+payloadLen] = byte(crc >> 16)
	out[3+payloadLen+1] = byte(crc >> 8)
	out[3+payloadLen+2] = byte(crc)
	return out
}

// ----------------------------------------------------------------
// RTCMProxy
// ----------------------------------------------------------------

// RTCMProxy listens on publicPort, connects to rtkrcv's outstr2 on internalPort,
// and replaces RTCM 1005/1006 base-station coordinates with the current CPP solution.
type RTCMProxy struct {
	publicAddr   string
	internalAddr string

	mu  sync.RWMutex
	pos *ecefXYZ // nil until first CPP convergence
}

func newRTCMProxy(publicPort, internalPort int) *RTCMProxy {
	return &RTCMProxy{
		publicAddr:   fmt.Sprintf(":%d", publicPort),
		internalAddr: fmt.Sprintf("127.0.0.1:%d", internalPort),
	}
}

// UpdatePos updates the base-station ECEF position from a CPP LLH solution.
func (p *RTCMProxy) UpdatePos(lat, lon, h float64) {
	ecef := llhToECEF(lat, lon, h)
	p.mu.Lock()
	p.pos = &ecef
	p.mu.Unlock()
}

func (p *RTCMProxy) getPos() *ecefXYZ {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pos
}

// Serve starts accepting clients until stopCh is closed.
func (p *RTCMProxy) Serve(stopCh <-chan struct{}) {
	ln, err := net.Listen("tcp", p.publicAddr)
	if err != nil {
		return
	}
	go func() {
		<-stopCh
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go p.handleClient(conn, stopCh)
	}
}

func (p *RTCMProxy) handleClient(client net.Conn, stopCh <-chan struct{}) {
	defer client.Close()

	// Wait until rtkrcv's logstr1 is ready.
	var upstream net.Conn
	for {
		var err error
		upstream, err = net.DialTimeout("tcp", p.internalAddr, 2*time.Second)
		if err == nil {
			break
		}
		select {
		case <-stopCh:
			return
		case <-time.After(time.Second):
		}
	}
	defer upstream.Close()

	// Close upstream when stopped.
	go func() {
		<-stopCh
		upstream.Close()
	}()

	// Protect concurrent writes (frame forwarder + synthetic 1005 injector).
	var writeMu sync.Mutex
	safeWrite := func(data []byte) error {
		writeMu.Lock()
		_, err := client.Write(data)
		writeMu.Unlock()
		return err
	}

	// Inject synthetic RTCM 1005 frame immediately (if CPP pos known) and every 10 s.
	// This ensures InjectRelay on the rover side always gets a non-zero ECEF position
	// even when the rover receiver doesn't transmit its own 1005/1006 messages.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		if pos := p.getPos(); pos != nil {
			safeWrite(build1005Frame(*pos)) //nolint:errcheck
		}
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if pos := p.getPos(); pos != nil {
					safeWrite(build1005Frame(*pos)) //nolint:errcheck
				}
			}
		}
	}()

	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		n, err := upstream.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				frame, rest, ok := nextRTCMFrame(buf)
				if !ok {
					break
				}
				buf = rest
				mt := rtcm3MsgType(frame)
				if mt == 1005 || mt == 1006 {
					if pos := p.getPos(); pos != nil {
						frame = patchBasePos(frame, *pos)
					}
				}
				if safeWrite(frame) != nil {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}
