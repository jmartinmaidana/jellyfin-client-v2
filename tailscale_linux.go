//go:build !windows

package main

import (
	"fmt"
	"net"
	"time"
)

func ensureNativeTailscale(authKey, targetHostPort string) error {
	conn, err := net.DialTimeout("tcp", targetHostPort, 3*time.Second)
	if err != nil {
		return fmt.Errorf("en Linux asegurate de tener tailscaled activo: %w", err)
	}
	conn.Close()
	return nil
}