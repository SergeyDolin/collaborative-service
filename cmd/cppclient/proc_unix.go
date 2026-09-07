//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachedProcess возвращает SysProcAttr, который запускает процесс
// в новой сессии (Setsid) без управляющего терминала.
func detachedProcess() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// killProcess отправляет SIGKILL процессу
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
