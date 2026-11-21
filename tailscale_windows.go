//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	tailscaleExePath = `C:\Program Files\Tailscale\tailscale.exe`
	msiFileName      = "tailscale-setup-amd64.msi"
)

func hideCmdWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

func ensureNativeTailscale(authKey, targetHostPort string) error {
	// 1. Verificar si Tailscale ya está instalado; si no, instalar silenciosamente
	if _, err := os.Stat(tailscaleExePath); err != nil {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("no se pudo determinar la ruta del ejecutable: %w", err)
		}
		msiPath := filepath.Join(filepath.Dir(exePath), msiFileName)
		if _, err := os.Stat(msiPath); err != nil {
			return fmt.Errorf("no se encontró el instalador %s junto al ejecutable", msiFileName)
		}

		installCmd := exec.Command("msiexec", "/i", msiPath, "/quiet", "/norestart")
		hideCmdWindow(installCmd)
		if err := installCmd.Run(); err != nil {
			return fmt.Errorf("falló la instalación de Tailscale: %w", err)
		}
		time.Sleep(3 * time.Second)
	}

	// 2. Asegurar que el servicio de Windows esté iniciado
	scCmd := exec.Command("net", "start", "Tailscale")
	hideCmdWindow(scCmd)
	_ = scCmd.Run()

	// 3. Autenticar y levantar el túnel sin alterar el DNS del usuario
	upCmd := exec.Command(
		tailscaleExePath,
		"up",
		"--authkey="+authKey,
		"--accept-dns=false",
		"--unattended",
	)
	hideCmdWindow(upCmd)
	if out, err := upCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("error ejecutando tailscale up (%s): %w", string(out), err)
	}

	// 4. Esperar conectividad TCP real con tu CachyOS
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", targetHostPort, 2*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("tiempo de espera agotado conectando con %s", targetHostPort)
}