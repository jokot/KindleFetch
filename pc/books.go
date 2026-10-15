package main

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type Book struct {
	Source    string // "zlib" or "lgli"
	Title     string
	Author    string
	Ext       string
	Size      string
	Year      string
	Language  string
	Publisher string
	MD5       string
	ZlibID    string
	ZlibHash  string
	URL       string
}

func (b Book) Summary() string {
	parts := []string{}
	if b.Publisher != "" && b.Year != "" {
		parts = append(parts, b.Publisher+" ("+b.Year+")")
	} else if b.Publisher != "" || b.Year != "" {
		parts = append(parts, b.Publisher+b.Year)
	}
	for _, p := range []string{b.Language, b.Ext, b.Size} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " - ")
}

// fileName builds "Title - Author.ext". Z-Library's own file names carry a
// "(z-library.sk, 1lib.sk, z-lib.sk)" suffix, so the name is built from the
// record instead.
func (b Book) fileName() string {
	name := b.Title
	if b.Author != "" {
		author := b.Author
		// Long multi-author strings make unwieldy file names; keep the first.
		if i := strings.IndexAny(author, ";,"); i > 0 && len(author) > 40 {
			author = author[:i]
		}
		name += " - " + author
	}
	name = sanitizeFileName(name)
	if name == "" {
		name = "book"
	}
	ext := strings.ToLower(sanitizeFileName(b.Ext))
	if ext == "" {
		ext = "bin"
	}
	return name + "." + ext
}

// sanitizeFileName removes what Windows, macOS or the Kindle's FAT32 volume
// would reject, and caps the length so the full path stays well clear of
// Windows' 260-character limit.
func sanitizeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r):
			return '_'
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = strings.TrimSpace(string(r[:120]))
	}
	return strings.TrimRight(s, ". ")
}

// downloadBook fetches b from the given source into dir and returns its path.
func downloadBook(cfg *Config, b Book, source, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var link string
	var err error
	header := http.Header{}
	switch source {
	case "zlib":
		link, err = zlibFileLink(cfg, b)
		header.Set("Cookie", cfg.zlibCookieHeader())
	case "lgli":
		link, err = libgenFileLink(cfg, b)
	default:
		err = errors.New("unknown source " + source)
	}
	if err != nil {
		return "", err
	}

	path := uniquePath(filepath.Join(dir, b.fileName()))
	fmt.Printf("\nDownloading %s\n", filepath.Base(path))
	if err := downloadTo(link, path, header); err != nil {
		return "", err
	}
	return path, nil
}

// uniquePath appends " (2)", " (3)"... rather than overwrite an existing book.
func uniquePath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 2; ; i++ {
		p := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
	}
}

var bookExts = map[string]bool{
	".epub": true, ".azw3": true, ".azw": true, ".mobi": true, ".pdf": true,
	".fb2": true, ".txt": true, ".djvu": true, ".cbz": true, ".cbr": true,
	".kfx": true, ".doc": true, ".docx": true, ".rtf": true,
}

// listBooks returns the book files directly inside dir, sorted by name.
func listBooks(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && bookExts[strings.ToLower(filepath.Ext(e.Name()))] {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}
