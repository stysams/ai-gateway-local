package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func ensureGateway(port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	if gatewayReachable(url) {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate desktop executable: %w", err)
	}
	cmd := exec.Command(executable, "serve")
	prepareDetached(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start gateway process: %w", err)
	}
	_ = cmd.Process.Release()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if gatewayReachable(url) {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("management API did not become ready at %s", url)
}

func gatewayReachable(url string) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
