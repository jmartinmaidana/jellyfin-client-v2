package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"strconv"
	"strings"
	"sync"
	"time"

	webview "github.com/webview/webview_go"
)

var (
	TailscaleAuthKey = ""
	RemoteTargetURL  = "http://100.64.0.1:8096"
	LocalHostsRaw    = "192.168.1.37:8096,192.168.1.5:8096"
)

var localCandidates = []string{
	"192.168.1.37:8096", // Ethernet
	"192.168.1.5:8096",  // Wi-Fi
}

var playbackRegex = regexp.MustCompile(`(?i)^/videos/([a-zA-Z0-9-]+)/(?:[^/]+/)?(master\.m3u8|main\.m3u8|stream)`)

var (
	lastPlayedID string
	playMu       sync.Mutex
)

func resolveLocalTarget() (string, bool) {
	rawList := strings.Split(LocalHostsRaw, ",")
	var candidates []string
	for _, h := range rawList {
		trimmed := strings.TrimSpace(h)
		if trimmed != "" {
			candidates = append(candidates, trimmed)
		}
	}
	if len(candidates) == 0 {
		return "", false
	}

	ch := make(chan string, len(candidates))
	for _, hostPort := range candidates {
		go func(hp string) {
			conn, err := net.DialTimeout("tcp", hp, 200*time.Millisecond)
			if err == nil {
				conn.Close()
				ch <- "http://" + hp
			}
		}(hostPort)
	}

	select {
	case url := <-ch:
		return url, true
	case <-time.After(220 * time.Millisecond):
		return "", false
	}
}

func main() {
	forceRemote := flag.Bool("remote", false, "Forzar conexión remota por Tailscale nativo")
	debugMode := flag.Bool("debug", false, "Mostrar registros detallados de red y HTTP")
	flag.Parse()

	target := RemoteTargetURL

	if !*forceRemote {
		if localTarget, ok := resolveLocalTarget(); ok {
			target = localTarget
			log.Printf("[RED] Servidor detectado en LAN local: %s", target)
		}
	}

	if target == RemoteTargetURL {
		if TailscaleAuthKey == "" {
			log.Fatal("Error: TailscaleAuthKey no configurada. Compilá el binario con .env o usá la red local.")
		}
		remoteHostPort := strings.TrimPrefix(strings.TrimPrefix(RemoteTargetURL, "http://"), "https://")
		log.Println("[1/2] Configurando red privada Tailscale nativa...")
		if err := ensureNativeTailscale(TailscaleAuthKey, remoteHostPort); err != nil {
			log.Fatalf("Error conectando Tailscale nativo: %v", err)
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("Error reservando puerto local: %v", err)
	}
	localURL := "http://" + listener.Addr().String()

	targetURL, _ := url.Parse(target)
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
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

	var w webview.WebView

	handler := http.HandlerFunc(func(wHttp http.ResponseWriter, r *http.Request) {
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
				playSessionID := q.Get("PlaySessionId")
				if playSessionID == "" {
					playSessionID = fmt.Sprintf("jf-mpv-%d", time.Now().UnixNano())
				}

				var startSeconds float64
				if ticksStr := q.Get("StartTimeTicks"); ticksStr != "" {
					if ticks, err := strconv.ParseInt(ticksStr, 10, 64); err == nil && ticks > 0 {
						startSeconds = float64(ticks) / 10_000_000.0
					}
				}

				directStreamURL := fmt.Sprintf(
					"%s/Videos/%s/stream?static=true&MediaSourceId=%s&api_key=%s",
					target, itemID, mediaSourceID, apiKey,
				)
				log.Printf("[MPV] Direct Play ítem %s (inicio: %.1fs)...", itemID, startSeconds)

				go launchMPVWithSync(localURL, directStreamURL, itemID, mediaSourceID, playSessionID, apiKey, startSeconds, func() {
					if w != nil {
						w.Dispatch(func() {
							w.Eval("if (window.Emby && window.Emby.Page) { window.location.reload(); }")
						})
					}
				})
			}

			wHttp.WriteHeader(http.StatusForbidden)
			return
		}

		proxy.ServeHTTP(wHttp, r)
	})

	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(listener)
	}()

	log.Printf("[2/2] Verificando motor gráfico y abriendo cliente Jellyfin...")

	if err := ensureWebView2(); err != nil {
		log.Fatalf("Error instalando WebView2 Runtime: %v", err)
	}

	w = webview.New(false)
	defer w.Destroy()
	setupWindowDecorations(w.Window())
	w.SetSize(1280, 720, webview.HintNone)

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

	log.Println("Cerrando aplicación...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

func getMPVBinaryPath() string {
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		localMPVWin := filepath.Join(exeDir, "mpv.exe")
		if _, err := os.Stat(localMPVWin); err == nil {
			return localMPVWin
		}
		localMPVUnix := filepath.Join(exeDir, "mpv")
		if _, err := os.Stat(localMPVUnix); err == nil {
			return localMPVUnix
		}
	}
	return "mpv"
}

func launchMPVWithSync(baseURL, streamURL, itemID, mediaSourceID, playSessionID, apiKey string, startSeconds float64, onFinish func()) {
	args := []string{
		"--hwdec=auto",
		"--gpu-api=auto",
		"--force-window=immediate",
		"--cache=yes",
		"--demuxer-max-bytes=150M",
		"--demuxer-readahead-secs=30",
		"--title=Jellyfin Direct Play",
		"--term-status-msg=JF_POS:${=time-pos}",
	}
	if startSeconds > 1.0 {
		args = append(args, fmt.Sprintf("--start=%.2f", startSeconds))
	}
	args = append(args, streamURL)

	cmd := exec.Command(getMPVBinaryPath(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("[MPV] Error abriendo pipe de telemetría: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("[MPV] Error iniciando mpv: %v", err)
		return
	}

	startTicks := int64(startSeconds * 10_000_000)
	reportJellyfinSession(baseURL, "/Sessions/Playing", apiKey, itemID, mediaSourceID, playSessionID, startTicks)

	var (
		posMu       sync.Mutex
		currentSecs = startSeconds
		stopTicker  = make(chan struct{})
	)

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Split(scanLinesOrCR)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "JF_POS:") {
				valStr := strings.TrimPrefix(line, "JF_POS:")
				if val, err := strconv.ParseFloat(valStr, 64); err == nil && val > 0 {
					posMu.Lock()
					currentSecs = val
					posMu.Unlock()
				}
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				posMu.Lock()
				ticks := int64(currentSecs * 10_000_000)
				posMu.Unlock()
				reportJellyfinSession(baseURL, "/Sessions/Playing/Progress", apiKey, itemID, mediaSourceID, playSessionID, ticks)
			case <-stopTicker:
				return
			}
		}
	}()

	_ = cmd.Wait()
	close(stopTicker)

	posMu.Lock()
	finalSecs := currentSecs
	finalTicks := int64(finalSecs * 10_000_000)
	posMu.Unlock()

	log.Printf("[SYNC] Guardando progreso en %.1f segundos (%.1f min)...", finalSecs, finalSecs/60.0)
	reportJellyfinSession(baseURL, "/Sessions/Playing/Stopped", apiKey, itemID, mediaSourceID, playSessionID, finalTicks)

	if onFinish != nil {
		onFinish()
	}
}

func reportJellyfinSession(baseURL, endpoint, apiKey, itemID, mediaSourceID, playSessionID string, positionTicks int64) {
	if apiKey == "" {
		return
	}

	payload := map[string]any{
		"ItemId":        itemID,
		"MediaSourceId": mediaSourceID,
		"PlaySessionId": playSessionID,
		"PositionTicks": positionTicks,
		"PlayMethod":    "DirectStream",
		"CanSeek":       true,
	}
	body, _ := json.Marshal(payload)

	reqURL := fmt.Sprintf("%s%s?api_key=%s", baseURL, endpoint, apiKey)
	req, err := http.NewRequest("POST", reqURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Emby-Authorization", fmt.Sprintf(`MediaBrowser Client="Jellyfin Unified", Device="Desktop", DeviceId="jf-unified-client", Version="1.0.0", Token="%s"`, apiKey))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func scanLinesOrCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' || data[i] == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
