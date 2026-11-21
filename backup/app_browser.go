package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"tailscale.com/tsnet"
)

const (
	TailscaleAuthKey = "tskey-auth-kHsAcMN9NX11CNTRL-ak2WVqwEgMagDTKWNjFjMa3LXdAyq6E7H"
	RemoteTargetURL  = "http://100.119.226.126:8096"
	LocalTargetURL   = "http://192.168.1.37:8096"
)

// Expresión regular para detectar cuando el cliente solicita iniciar un stream de video
var videoStreamRegex = regexp.MustCompile(`^/Videos/([a-zA-Z0-9]+)/stream`)

var (
	lastPlayedID string
	playMu       sync.Mutex
)

func isLocalServerReachable() bool {
	conn, err := net.DialTimeout("tcp", "192.168.1.37:8096", 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func main() {
	forceRemote := flag.Bool("remote", false, "Forzar conexión remota por Tailscale tsnet")
	flag.Parse()

	var transport *http.Transport
	target := LocalTargetURL

	if *forceRemote || !isLocalServerReachable() {
		log.Println("[1/2] Estableciendo túnel cifrado Tailscale...")
		stateDir, err := os.MkdirTemp("", "jf-tsnet-*")
		if err != nil {
			log.Fatalf("Error creando directorio temporal: %v", err)
		}
		defer os.RemoveAll(stateDir)

		srv := &tsnet.Server{
			Hostname:  "jellyfin-guest",
			AuthKey:   TailscaleAuthKey,
			Dir:       filepath.Join(stateDir, "state"),
			Ephemeral: true,
			Logf:      func(format string, args ...any) {},
		}
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if _, err := srv.Up(ctx); err != nil {
			log.Fatalf("Error conectando al túnel: %v", err)
		}

		transport = &http.Transport{
			DialContext:           srv.Dial,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		target = RemoteTargetURL
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Error reservando puerto local: %v", err)
	}
	localURL := "http://" + listener.Addr().String()

	targetURL, _ := url.Parse(target)
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	if transport != nil {
		proxy.Transport = transport
	}
	proxy.FlushInterval = -1

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host
	}

	// Handler envolvente: intercepta peticiones de reproducción web y las lanza en mpv (Direct Play)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")

		// Si la petición viene del navegador web (no de mpv) pidiendo un stream de video:
		if !strings.Contains(ua, "mpv") && videoStreamRegex.MatchString(r.URL.Path) {
			matches := videoStreamRegex.FindStringSubmatch(r.URL.Path)
			itemID := matches[1]

			playMu.Lock()
			alreadyLaunching := (lastPlayedID == itemID)
			if !alreadyLaunching {
				lastPlayedID = itemID
				// Permitir volver a abrir el mismo video después de 5 segundos
				go func() {
					time.Sleep(5 * time.Second)
					playMu.Lock()
					if lastPlayedID == itemID {
						lastPlayedID = ""
					}
					playMu.Unlock()
				}()
			}
			playMu.Unlock()

			if !alreadyLaunching {
				// Construir URL directa (static=true evita transcodificación en el servidor)
				apiKey := r.URL.Query().Get("api_key")
				directStreamURL := fmt.Sprintf("%s/Videos/%s/stream?static=true&api_key=%s", localURL, itemID, apiKey)
				log.Printf("[PLAYER] Abriendo video %s en mpv (Direct Play)...", itemID)
				go launchMPV(directStreamURL)
			}

			// Responder 204 al navegador para que no se quede colgado cargando
			w.WriteHeader(http.StatusNoContent)
			return
		}

		proxy.ServeHTTP(w, r)
	})

	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()

	log.Printf("[2/2] Abriendo interfaz gráfica en %s...", localURL)
	uiCmd := launchAppWindow(localURL)

	done := make(chan struct{})
	if uiCmd != nil {
		go func() {
			_ = uiCmd.Wait()
			close(done)
		}()
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	select {
	case <-done:
		log.Println("Ventana cerrada. Desconectando túnel...")
	case <-sigChan:
		log.Println("Interrupción recibida. Desconectando túnel...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

func launchMPV(streamURL string) {
	// --hwdec=auto activa aceleración por GPU; --force-window=immediate abre la ventana al instante
	cmd := exec.Command("mpv",
		"--hwdec=auto",
		"--force-window=immediate",
		"--title=Jellyfin Player",
		streamURL,
	)
	if err := cmd.Run(); err != nil {
		log.Printf("[PLAYER] Error ejecutando mpv: %v", err)
	}
}

func launchAppWindow(appURL string) *exec.Cmd {
	profileDir, _ := os.MkdirTemp("", "jf-ui-profile-*")
	// --new-window abre una ventana estándar con botones de minimizar/maximizar/cerrar
	args := []string{
		"--new-window",
		"--user-data-dir=" + profileDir,
		"--window-size=1280,720",
		"--no-first-run",
		appURL,
	}

	var candidates []string
	if runtime.GOOS == "windows" {
		candidates = []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		}
	} else {
		candidates = []string{"brave", "chromium", "google-chrome-stable", "microsoft-edge-stable"}
	}

	for _, bin := range candidates {
		path, err := exec.LookPath(bin)
		if err == nil {
			cmd := exec.Command(path, args...)
			if err := cmd.Start(); err == nil {
				return cmd
			}
		}
	}

	if runtime.GOOS == "windows" {
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", appURL).Start()
	} else {
		_ = exec.Command("xdg-open", appURL).Start()
	}
	return nil
}
