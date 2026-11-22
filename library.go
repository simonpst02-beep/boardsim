package main

// Bibliothèque locale : BoardSim liste les schémas et vues de carte rangés
// dans Documents\BoardSim\Bibliothèque, y compris à l'intérieur des .zip,
// sans rien extraire ni envoyer ailleurs.

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type libItem struct {
	Kind  string `json:"kind"` // pdf, board, asc, fz
	ID    string `json:"id"`   // chemin relatif ; « archive.zip!dossier/fichier » dans un zip
	Name  string `json:"name"`
	Group string `json:"group"`
	Size  int64  `json:"size"`
}

func libDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	p := filepath.Join(home, "Documents", "BoardSim", "Bibliothèque")
	_ = os.MkdirAll(p, 0o755)
	return p
}

var boardExt = map[string]bool{".brd": true, ".brd2": true, ".bdv": true, ".bvr": true, ".bv": true, ".cad": true, ".gcd": true, ".gencad": true}

func classify(inner string) (kind, name string) {
	base := path.Base(inner)
	low := strings.ToLower(base)
	ext := path.Ext(low)
	switch {
	case low == "pins.asc":
		dir := path.Dir(inner)
		name = path.Base(dir)
		if name == "." || name == "/" {
			name = "Carte ASC"
		}
		return "asc", name
	case ext == ".pdf":
		return "pdf", strings.TrimSuffix(base, path.Ext(base))
	case ext == ".fz":
		return "fz", strings.TrimSuffix(base, path.Ext(base))
	case boardExt[ext]:
		return "board", strings.TrimSuffix(base, path.Ext(base))
	}
	return "", ""
}

func groupOf(inner, fallback string) string {
	parts := strings.Split(strings.Trim(inner, "/"), "/")
	if len(parts) > 1 {
		return parts[0]
	}
	return fallback
}

func scanLibrary() []libItem {
	root := libDir()
	var items []libItem
	add := func(id, inner string, size int64, fallback string) {
		kind, name := classify(inner)
		if kind == "" {
			return
		}
		if kind == "asc" {
			id = strings.TrimSuffix(id, path.Base(inner)) // l'identifiant d'une carte ASC est son dossier
		}
		items = append(items, libItem{Kind: kind, ID: id, Name: name, Group: groupOf(inner, fallback), Size: size})
	}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		info, _ := d.Info()
		if strings.EqualFold(filepath.Ext(p), ".zip") {
			zr, err := zip.OpenReader(p)
			if err != nil {
				return nil
			}
			defer zr.Close()
			fb := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
			for _, f := range zr.File {
				if strings.HasSuffix(f.Name, "/") || strings.Contains(f.Name, "__MACOSX") {
					continue
				}
				add(rel+"!"+f.Name, f.Name, int64(f.UncompressedSize64), fb)
			}
			return nil
		}
		var size int64
		if info != nil {
			size = info.Size()
		}
		add(rel, rel, size, "Divers")
		return nil
	})
	sort.Slice(items, func(i, j int) bool {
		if items[i].Group != items[j].Group {
			return strings.ToLower(items[i].Group) < strings.ToLower(items[j].Group)
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items
}

// safeLocal renvoie le chemin disque d'un fichier de la bibliothèque, sans jamais en sortir.
func safeLocal(rel string) (string, error) {
	root := libDir()
	p := filepath.Join(root, filepath.FromSlash(rel))
	r, err := filepath.Rel(root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("chemin refusé")
	}
	return p, nil
}

const maxLibFile = 400 << 20

func readLib(id string) ([]byte, error) {
	if zipRel, inner, ok := strings.Cut(id, "!"); ok {
		zp, err := safeLocal(zipRel)
		if err != nil {
			return nil, err
		}
		zr, err := zip.OpenReader(zp)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.Name == inner {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxLibFile))
			}
		}
		return nil, errors.New("fichier introuvable dans l'archive")
	}
	p, err := safeLocal(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxLibFile))
}

// readASC assemble format.asc, pins.asc et nails.asc d'un dossier en un seul texte.
func readASC(dirID string) ([]byte, error) {
	var names []string
	if zipRel, inner, ok := strings.Cut(dirID, "!"); ok {
		zp, err := safeLocal(zipRel)
		if err != nil {
			return nil, err
		}
		zr, err := zip.OpenReader(zp)
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Dir(f.Name)+"/" == inner || (inner == "" && path.Dir(f.Name) == ".") {
				names = append(names, zipRel+"!"+f.Name)
			}
		}
		zr.Close()
	} else {
		p, err := safeLocal(dirID)
		if err != nil {
			return nil, err
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		for _, e := range ents {
			names = append(names, path.Join(strings.TrimSuffix(dirID, "/"), e.Name()))
		}
	}
	var out strings.Builder
	for _, want := range []string{"format", "pins", "nails"} {
		for _, n := range names {
			if strings.EqualFold(path.Base(n), want+".asc") {
				b, err := readLib(n)
				if err != nil {
					return nil, err
				}
				out.WriteString("<<" + want + ".asc>>\n")
				out.Write(b)
				out.WriteString("\n")
			}
		}
	}
	if !strings.Contains(out.String(), "<<pins.asc>>") {
		return nil, errors.New("pins.asc introuvable")
	}
	return []byte(out.String()), nil
}

func handleLibrary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"dir": libDir(), "items": scanLibrary()})
}

func handleLibFile(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	var b []byte
	var err error
	if r.URL.Query().Get("asc") == "1" {
		b, err = readASC(id)
	} else {
		b, err = readLib(id)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(b)
}

func handleLibOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "méthode", http.StatusMethodNotAllowed)
		return
	}
	dir := libDir()
	if runtime.GOOS == "windows" {
		_ = exec.Command("explorer", dir).Start()
	} else {
		_ = exec.Command("xdg-open", dir).Start()
	}
	w.WriteHeader(http.StatusNoContent)
}
