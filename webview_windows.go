//go:build windows
package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func isWebView2Installed() bool {
	const webView2GUID = "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

	keys := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + webView2GUID},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webView2GUID},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\` + webView2GUID},
	}

	for _, k := range keys {
		regKey, err := registry.OpenKey(k.root, k.path, registry.QUERY_VALUE)
		if err == nil {
			pv, _, errVal := regKey.GetStringValue("pv")
			regKey.Close()
			if errVal == nil && pv != "" && pv != "0.0.0.0" {
				return true
			}
		}
	}
	return false
}

func ensureWebView2() error {
	if isWebView2Installed() {
		return nil
	}

	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	appDir := filepath.Dir(exePath)

	installerPath := filepath.Join(appDir, "MicrosoftEdgeWebView2Setup.exe")
	if _, err := os.Stat(installerPath); err != nil {
		return err
	}

	log.Println("Instalando Microsoft Edge WebView2 Runtime (única vez)...")
	cmd := exec.Command(installerPath, "/silent", "/install")
	return cmd.Run()
}