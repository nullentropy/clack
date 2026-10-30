//go:build !darwin

package main

import (
	"os/exec"
	"runtime"
)

func notify(title, body string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	cmd := exec.Command("notify-send", "--app-name=clack", title, body)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
