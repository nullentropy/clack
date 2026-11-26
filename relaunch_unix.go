//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

var exitProcess = os.Exit

func relaunch(exe string) error {
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	exitProcess(0)
	return nil
}
