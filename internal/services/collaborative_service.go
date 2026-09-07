package services

import (
	"fmt"

	"collaborative/internal/model"
)

// BuildStr2strCmd генерирует команду str2str для ретрансляции данных устройства на выделенный порт.
//
// Логика:
//   - TCP/IP: str2str подключается к устройству (tcpcli) и пробрасывает поток на наш TCP-сервер (tcpsvr)
//   - NTRIP:  str2str забирает поток с каста (ntrip) и пробрасывает на наш TCP-сервер (tcpsvr)
func BuildStr2strCmd(sess *model.CollaborativeSession) string {
	switch sess.ConnectionType {
	case model.ConnectionTypeTCP:
		return fmt.Sprintf(
			"str2str -in tcpcli://%s:%d -out tcpsvr://:%d",
			sess.TCPHost, sess.TCPPort, sess.AssignedPort,
		)
	case model.ConnectionTypeNTRIP:
		return fmt.Sprintf(
			"str2str -in ntrip://%s:%s@%s:%d/%s -out tcpsvr://:%d",
			sess.NTRIPUser, sess.NTRIPPass,
			sess.NTRIPHost, sess.NTRIPPort, sess.NTRIPMountpoint,
			sess.AssignedPort,
		)
	}
	return ""
}
