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
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	webview "github.com/webview/webview_go"
	"tailscale.com/tsnet"
)

const (
	// Reemplazar con la nueva Auth Key generada (Reusable + Ephemeral + tag:jellyfin-app)
	TailscaleAuthKey = "tskey-auth-kHsAcMN9NX11CNTRL-ak2WVqwEgMagDTKWNjFjMa3LXdAyq6E7H"
	RemoteTargetURL  = "http://100.119.226.126:8096"
	LocalTargetURL   = "http://192.168.1.37:8096"
)

var playbackRegex = regexp.MustCompile(`(?i)^/videos/([a-zA-Z0-9-]+)/(?:[^/]+/)?(master\.m3u8|main\.m3u8|stream)`)

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
	os.Setenv("GTK_CSD", "0")

	forceRemote := flag.Bool("remote", false, "Forzar conexión remota por Tailscale tsnet")
	debugMode := flag.Bool("debug", false, "Mostrar registros detallados de red y HTTP")
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
			UserLogf:  func(format string, args ...any) {},
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
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if *debugMode {
			log.Printf("[PROXY] Conexión cerrada: %v", err)
		}
	}

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = targetURL.Host
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := strings.ToLower(r.Header.Get("User-Agent"))

		if *debugMode && strings.Contains(strings.ToLower(r.URL.Path), "/videos/") {
			log.Printf("[DEBUG-HTTP] %s (UA: %s)", r.URL.Path, ua)
		}

		if !strings.Contains(ua, "mpv") && playbackRegex.MatchString(r.URL.Path) {
			matches := playbackRegex.FindStringSubmatch(r.URL.Path)
			itemID := matches[1]

			playMu.Lock()
			alreadyLaunching := (lastPlayedID == itemID)
			if !alreadyLaunching {
				lastPlayedID = itemID
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
				q := r.URL.Query()
				apiKey := q.Get("api_key")
				if apiKey == "" {
					apiKey = q.Get("ApiKey")
				}
				mediaSourceID := q.Get("MediaSourceId")
				if mediaSourceID == "" {
					mediaSourceID = itemID
				}

				directStreamURL := fmt.Sprintf(
					"%s/Videos/%s/stream?static=true&MediaSourceId=%s&api_key=%s",
					localURL, itemID, mediaSourceID, apiKey,
				)
				log.Printf("[MPV] Reproduciendo en Direct Play (ítem %s)...", itemID)
				go launchMPV(directStreamURL)
			}

			w.WriteHeader(http.StatusForbidden)
			return
		}

		proxy.ServeHTTP(w, r)
	})

	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()

	log.Printf("[2/2] Abriendo cliente Jellyfin...")

	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Jellyfin")
	w.SetSize(1280, 720, webview.HintNone)

	// Limpiar automáticamente cualquier diálogo de error del reproductor HTML cuando mpv toma el video
	w.Init(`
	(function() {
		const observer = new MutationObserver(() => {
			const dialog = document.querySelector('.dialogContainer .btnCancel, .dialogContainer button[data-id="ok"]');
			if (dialog && document.body.innerText.includes('Error')) {
				dialog.click();
				setTimeout(() => {
					const backBtn = document.querySelector('.headerBackButton');
					if (backBtn) backBtn.click();
				}, 100);
			}
		});
		window.addEventListener('DOMContentLoaded', () => {
			observer.observe(document.body, { childList: true, subtree: true });
		});
	})();
	`)

	w.Navigate(localURL)
	w.Run()

	log.Println("Cerrando aplicación y liberando túnel...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

func launchMPV(streamURL string) {
	cmd := exec.Command("mpv",
		"--hwdec=auto-safe",
		"--force-window=immediate",
		"--cache=yes",
		"--demuxer-max-bytes=150M",
		"--demuxer-readahead-secs=30",
		"--title=Jellyfin Direct Play",
		streamURL,
	)
	if err := cmd.Run(); err != nil {
		log.Printf("[MPV] Error al ejecutar reproductor: %v", err)
	}
}
