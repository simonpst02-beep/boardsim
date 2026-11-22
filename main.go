// BoardSim — version bureau pour Windows.
//
// Le programme contient toute l'interface (dossier web/), la sert sur
// http://127.0.0.1:47831 et l'ouvre dans une fenêtre d'application Edge
// (ou Chrome). Le carnet de réparation est enregistré dans
// %APPDATA%\BoardSim\carnet.json. Les mises à jour viennent des
// « Releases » du dépôt GitHub indiqué à la compilation (-X main.repo=…).
package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

// Remplis à la compilation par GitHub Actions.
var (
	version = "v0.0.0-dev"
	repo    = "" // propriétaire/dépôt, ex. "monpseudo/atelier-cartes"
)

const (
	port    = 47831
	appID   = "boardsim"
	exeName = "BoardSim.exe"
)

var (
	mu       sync.Mutex
	lastPing = time.Now()
	pinged   bool
)

func dataDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	p := filepath.Join(d, "BoardSim")
	if _, err := os.Stat(p); os.IsNotExist(err) { // reprise du carnet de la version « Atelier Cartes »
		if b, err := os.ReadFile(filepath.Join(d, "AtelierCartes", "carnet.json")); err == nil {
			_ = os.MkdirAll(p, 0o755)
			_ = os.WriteFile(filepath.Join(p, "carnet.json"), b, 0o644)
		}
	}
	_ = os.MkdirAll(p, 0o755)
	return p
}

func main() {
	if f, err := os.OpenFile(filepath.Join(dataDir(), "journal.txt"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		log.SetOutput(f)
	}
	afterUpdate := len(os.Args) > 1 && os.Args[1] == "--after-update"
	exe, _ := os.Executable()
	_ = os.Remove(exe + ".old") // reste d'une mise à jour précédente

	url := fmt.Sprintf("http://127.0.0.1:%d/", port)

	// Déjà ouvert ? On montre simplement une nouvelle fenêtre.
	if !afterUpdate && isRunning(url) {
		openWindow(url)
		return
	}

	var ln net.Listener
	var err error
	for i := 0; i < 30; i++ { // après une mise à jour, l'ancienne version libère le port
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		log.Printf("port %d indisponible : %v", port, err)
		return
	}

	// Certains Windows déclarent .js en text/plain : on force le bon type.
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")

	sub, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	files := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
	mux.HandleFunc("/api/ping", handlePing)
	mux.HandleFunc("/api/store", handleStore)
	mux.HandleFunc("/api/version", handleVersion)
	mux.HandleFunc("/api/update", handleUpdate)

	srv := &http.Server{Handler: guard(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	if !afterUpdate {
		openWindow(url)
	}
	watchdog()
}

// guard refuse toute requête qui ne vient pas de la fenêtre locale
// (protection contre les sites web qui tenteraient d'appeler l'API).
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Host
		if h != fmt.Sprintf("127.0.0.1:%d", port) && h != fmt.Sprintf("localhost:%d", port) {
			http.Error(w, "refusé", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Atelier") != "1" {
			http.Error(w, "refusé", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// watchdog ferme le programme quand la fenêtre est fermée (plus de « ping »).
func watchdog() {
	start := time.Now()
	for {
		time.Sleep(5 * time.Second)
		mu.Lock()
		idle, seen := time.Since(lastPing), pinged
		mu.Unlock()
		if (seen && idle > 40*time.Second) || (!seen && time.Since(start) > 3*time.Minute) {
			log.Printf("fenêtre fermée, arrêt")
			return
		}
	}
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	lastPing, pinged = time.Now(), true
	mu.Unlock()
	_, _ = io.WriteString(w, appID)
}

func isRunning(url string) bool {
	c := http.Client{Timeout: 800 * time.Millisecond}
	resp, err := c.Get(url + "api/ping")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return string(b) == appID
}

/* ---------- Carnet de réparation sur le disque ---------- */

func handleStore(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(dataDir(), "carnet.json")
	switch r.Method {
	case http.MethodGet:
		b, err := os.ReadFile(path)
		if err != nil || len(b) == 0 {
			b = []byte("{}")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	case http.MethodPut:
		b, err := io.ReadAll(io.LimitReader(r.Body, 512<<20))
		if err != nil || !json.Valid(b) {
			http.Error(w, "données invalides", http.StatusBadRequest)
			return
		}
		if old, err := os.ReadFile(path); err == nil && len(old) > 2 {
			_ = os.WriteFile(path+".bak", old, 0o644) // copie de secours
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "méthode", http.StatusMethodNotAllowed)
	}
}

/* ---------- Mises à jour depuis GitHub ---------- */

type release struct {
	Tag    string `json:"tag_name"`
	Body   string `json:"body"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

var (
	relMu    sync.Mutex
	relCache *release
	relTime  time.Time
)

func latestRelease() (*release, error) {
	if repo == "" {
		return nil, errors.New("aucun dépôt GitHub configuré")
	}
	relMu.Lock()
	defer relMu.Unlock()
	if relCache != nil && time.Since(relTime) < 10*time.Minute {
		return relCache, nil
	}
	api := "https://api.github.com"
	if v := os.Getenv("ATELIER_GITHUB_API"); v != "" { // pour les tests uniquement
		api = v
	}
	req, _ := http.NewRequest("GET", api+"/repos/"+repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "BoardSim/"+version)
	c := http.Client{Timeout: 15 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub a répondu %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	relCache, relTime = &rel, time.Now()
	return &rel, nil
}

func parseVer(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

func newer(a, b string) bool { // a > b ?
	x, y := parseVer(a), parseVer(b)
	for i := 0; i < 3; i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"version": version, "repo": repo, "update": false}
	if rel, err := latestRelease(); err == nil {
		out["latest"] = rel.Tag
		out["notes"] = rel.Body
		out["update"] = newer(rel.Tag, version)
	} else {
		out["error"] = err.Error()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func download(url string, max int64) ([]byte, error) {
	c := http.Client{Timeout: 5 * time.Minute}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "BoardSim/"+version)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("téléchargement : %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

func handleUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "méthode", http.StatusMethodNotAllowed)
		return
	}
	if err := applyUpdate(); err != nil {
		log.Printf("mise à jour : %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = io.WriteString(w, "ok")
	go func() { time.Sleep(800 * time.Millisecond); os.Exit(0) }()
}

func applyUpdate() error {
	rel, err := latestRelease()
	if err != nil {
		return err
	}
	if !newer(rel.Tag, version) {
		return errors.New("vous avez déjà la dernière version")
	}
	var exeURL, sumURL string
	for _, a := range rel.Assets {
		switch strings.ToLower(a.Name) {
		case strings.ToLower(exeName):
			exeURL = a.URL
		case strings.ToLower(exeName) + ".sha256":
			sumURL = a.URL
		}
	}
	if exeURL == "" || sumURL == "" {
		return errors.New("la version publiée ne contient pas BoardSim.exe et son empreinte .sha256")
	}
	bin, err := download(exeURL, 200<<20)
	if err != nil {
		return err
	}
	if len(bin) < 1<<20 || !bytes.HasPrefix(bin, []byte("MZ")) {
		return errors.New("le fichier téléchargé n'est pas un programme Windows valide")
	}
	sumTxt, err := download(sumURL, 1024)
	if err != nil {
		return err
	}
	want := strings.ToLower(strings.Fields(string(sumTxt) + " x")[0])
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return errors.New("empreinte SHA-256 incorrecte : mise à jour annulée")
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	newPath := exe + ".new"
	if err := os.WriteFile(newPath, bin, 0o755); err != nil {
		return fmt.Errorf("écriture impossible dans %s (placez le logiciel dans un dossier à vous, pas dans Program Files) : %v", filepath.Dir(exe), err)
	}
	_ = os.Remove(exe + ".old")
	if err := os.Rename(exe, exe+".old"); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("remplacement impossible : %v", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(exe+".old", exe)
		return fmt.Errorf("remplacement impossible : %v", err)
	}
	cmd := exec.Command(exe, "--after-update")
	cmd.Dir = filepath.Dir(exe)
	if err := cmd.Start(); err != nil {
		_ = os.Rename(exe, newPath) // on remet l'ancienne version
		_ = os.Rename(exe+".old", exe)
		_ = os.Remove(newPath)
		return fmt.Errorf("redémarrage impossible, ancienne version conservée : %v", err)
	}
	log.Printf("mise à jour %s -> %s", version, rel.Tag)
	return nil
}

/* ---------- Fenêtre ---------- */

func openWindow(url string) {
	if runtime.GOOS != "windows" {
		_ = exec.Command("xdg-open", url).Start()
		return
	}
	var cands []string
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		if base := os.Getenv(env); base != "" {
			cands = append(cands,
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`))
		}
	}
	for _, p := range cands {
		if _, err := os.Stat(p); err == nil {
			if exec.Command(p, "--app="+url, "--window-size=1440,900").Start() == nil {
				return
			}
		}
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
