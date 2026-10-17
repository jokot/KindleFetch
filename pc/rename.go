package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode"
)

// Renaming book files to "Title - Author.ext", ported from KindleDeDRM's
// "Rename EPUB files". EPUBs are named from the title and authors stored
// inside them; other books, and EPUBs without a title, keep their current
// name with symbols removed.

type epubMeta struct {
	Title   string
	Authors []string
}

type epubContainer struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfPackage struct {
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators []struct {
			Name string `xml:",chardata"`
			Role string `xml:"role,attr"` // EPUB 2: opf:role
			ID   string `xml:"id,attr"`
		} `xml:"creator"`
		Metas []struct {
			Refines  string `xml:"refines,attr"` // EPUB 3: <meta refines="#id" property="role">
			Property string `xml:"property,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
}

func readZipEntry(zr *zip.ReadCloser, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return io.ReadAll(r)
		}
	}
	return nil, fs.ErrNotExist
}

// decodeXML is lenient: publishers' OPF files use HTML entities and the odd
// Latin-1 declaration.
func decodeXML(data []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = func(charset string, r io.Reader) (io.Reader, error) {
		switch strings.ToLower(charset) {
		case "utf8", "us-ascii", "ascii":
			return r, nil
		case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
			b, err := io.ReadAll(r)
			runes := make([]rune, len(b))
			for i, c := range b {
				runes[i] = rune(c)
			}
			return strings.NewReader(string(runes)), err
		}
		return nil, fmt.Errorf("unsupported charset %s", charset)
	}
	return d.Decode(v)
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// epubMetadata reads the title and authors from an EPUB's OPF file. ok is
// false when the file is unreadable or has no title. Creators with a role
// other than author (translator, editor...) and Calibre's "Unknown" are left
// out.
func epubMetadata(path string) (m epubMeta, ok bool) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return m, false
	}
	defer zr.Close()

	data, err := readZipEntry(zr, "META-INF/container.xml")
	if err != nil {
		return m, false
	}
	var c epubContainer
	if decodeXML(data, &c) != nil || len(c.Rootfiles) == 0 {
		return m, false
	}
	opfPath := c.Rootfiles[0].FullPath
	if p, err := url.PathUnescape(opfPath); err == nil {
		opfPath = p
	}
	if data, err = readZipEntry(zr, opfPath); err != nil {
		return m, false
	}
	var opf opfPackage
	if decodeXML(data, &opf) != nil {
		return m, false
	}

	md := opf.Metadata
	if len(md.Titles) > 0 {
		m.Title = collapseSpace(md.Titles[0])
	}
	if m.Title == "" {
		return m, false
	}
	roles := map[string]string{}
	for _, meta := range md.Metas {
		if meta.Property == "role" && meta.Refines != "" {
			roles[strings.TrimPrefix(meta.Refines, "#")] = strings.TrimSpace(meta.Value)
		}
	}
	for _, cr := range md.Creators {
		role := cr.Role
		if role == "" && cr.ID != "" {
			role = roles[cr.ID]
		}
		if role != "" && role != "aut" {
			continue
		}
		if name := collapseSpace(cr.Name); name != "" && name != "Unknown" {
			m.Authors = append(m.Authors, name)
		}
	}
	return m, true
}

// Z-Library's own file names end in "(z-library.sk, 1lib.sk, z-lib.sk)" or
// "(Z-Library)".
var zlibSuffix = regexp.MustCompile(`(?i)\s*\((?:z-lib|z-library|1lib)[^)]*\)`)

// cleanFileStem tidies a name that has no metadata to replace it: every
// symbol except - ' , . & ( ) becomes a space, then sanitizeFileName applies.
func cleanFileStem(s string) string {
	s = zlibSuffix.ReplaceAllString(s, " ")
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) || strings.ContainsRune(" -',.&():’", r) {
			return r
		}
		return ' '
	}, s)
	return sanitizeFileName(s)
}

// renameTarget returns the name a book file should have ("" when nothing
// usable is left) and where it came from: "metadata" or "cleaned".
func renameTarget(path string) (name, source string) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".epub" {
		if m, ok := epubMetadata(path); ok {
			var authors []string
			for _, a := range m.Authors {
				if s := sanitizeFileName(a); s != "" {
					authors = append(authors, s)
				}
			}
			if stem := sanitizeFileName(m.Title); stem != "" {
				if len(authors) > 0 {
					stem = sanitizeFileName(stem + " - " + strings.Join(authors, " & "))
				}
				return stem + ext, "metadata"
			}
		}
	}
	stem := cleanFileStem(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if stem == "" {
		return "", "cleaned"
	}
	return stem + ext, "cleaned"
}

type renameItem struct {
	Path, Dir, Current, New, Source string
	Action                          string // "rename", "same" or "skip"
}

// renamePlan works out every file's new name without touching anything. A
// name already used in the folder (by another file, or earlier in the plan)
// gets " (2)", " (3)"...
func renamePlan(paths []string) []renameItem {
	taken := map[string]map[string]bool{} // folder -> lower-cased names in use
	var plan []renameItem
	for _, p := range paths {
		dir, cur := filepath.Dir(p), filepath.Base(p)
		names := taken[dir]
		if names == nil {
			names = map[string]bool{}
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				names[strings.ToLower(e.Name())] = true
			}
			taken[dir] = names
		}
		delete(names, strings.ToLower(cur))

		it := renameItem{Path: p, Dir: dir, Current: cur, Action: "rename"}
		it.New, it.Source = renameTarget(p)
		switch {
		case it.New == "":
			it.New, it.Action = cur, "skip"
		case it.New == cur:
			it.Action = "same"
		default:
			ext := filepath.Ext(it.New)
			stem := strings.TrimSuffix(it.New, ext)
			for n := 2; names[strings.ToLower(it.New)]; n++ {
				it.New = fmt.Sprintf("%s (%d)%s", stem, n, ext)
			}
		}
		names[strings.ToLower(it.New)] = true
		plan = append(plan, it)
	}
	return plan
}

// applyRename renames one planned file and its reading-progress sidecar. It
// never overwrites: a clash fails that one file.
func applyRename(it renameItem) error {
	if err := moveNoClobber(it.Path, filepath.Join(it.Dir, it.New)); err != nil {
		return err
	}
	// The Kindle and KOReader keep reading position and highlights in a
	// "<name>.sdr" folder next to the book; move it along so they follow.
	oldSdr := strings.TrimSuffix(it.Path, filepath.Ext(it.Path)) + ".sdr"
	if fi, err := os.Stat(oldSdr); err == nil && fi.IsDir() {
		_ = moveNoClobber(oldSdr, filepath.Join(it.Dir, strings.TrimSuffix(it.New, filepath.Ext(it.New))+".sdr"))
	}
	return nil
}

func moveNoClobber(src, dst string) error {
	var err error
	if strings.EqualFold(filepath.Base(src), filepath.Base(dst)) {
		// Only the letter case changes; Windows needs a detour through a
		// temporary name.
		tmp := filepath.Join(filepath.Dir(src), fmt.Sprintf(".kindlefetch-%d.tmp", time.Now().UnixNano()))
		if err = os.Rename(src, tmp); err == nil {
			if err = os.Rename(tmp, dst); err != nil {
				_ = os.Rename(tmp, src)
			}
		}
	} else if _, statErr := os.Lstat(dst); statErr == nil {
		return errors.New(filepath.Base(dst) + " already exists")
	} else {
		err = os.Rename(src, dst)
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		err = le.Err
	}
	return err
}

// renameCandidates lists the book files at path: the file itself, or the
// books in a folder (and its subfolders when recurse is set).
func renameCandidates(path string, recurse bool) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("not found: %s", path)
	}
	if !fi.IsDir() {
		if !bookExts[strings.ToLower(filepath.Ext(path))] {
			return nil, fmt.Errorf("%s is not a book file", filepath.Base(path))
		}
		return []string{path}, nil
	}
	if !recurse {
		return listBooks(path), nil
	}
	var out []string
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return nil
		case d.IsDir() && p != path && strings.HasPrefix(d.Name(), "."):
			return filepath.SkipDir
		case d.IsDir():
			out = append(out, listBooks(p)...)
		}
		return nil
	})
	return out, nil
}

// hasBookSubfolders reports whether any folder below dir holds a book.
func hasBookSubfolders(dir string) bool {
	found := errors.New("found")
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if len(listBooks(p)) > 0 {
			return found
		}
		return nil
	})
	return err == found
}

// cleanDroppedPath undoes what terminals add when a file is dragged into the
// window: quotes on Windows, backslash escapes ("My\ Books") on macOS.
func cleanDroppedPath(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(s); err != nil && strings.Contains(s, `\`) {
			var b strings.Builder
			for i := 0; i < len(s); i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
			}
			s = b.String()
		}
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(s, "~/") {
			s = filepath.Join(home, s[2:])
		}
	}
	return s
}

func renameMenu(cfg *Config) {
	clearScreen()
	fmt.Println("Rename book files")
	fmt.Println()
	fmt.Println(`Renames books to "Title - Author" using the title and author stored inside`)
	fmt.Println("each EPUB. Other files, and EPUBs without a title, keep their name with")
	fmt.Println("symbols removed. You see a preview before anything changes.")
	fmt.Println("Tip: drag a folder or book into this window to paste its path.")
	p := cleanDroppedPath(prompt(fmt.Sprintf("\nFolder or book file (Enter = %s, q = back): ", cfg.DownloadDir)))
	if strings.EqualFold(p, "q") {
		return
	}
	if p == "" {
		p = cfg.DownloadDir
	}
	renameFlow(p)
	pause()
}

// renameFlow previews, confirms, then renames the books at path.
func renameFlow(path string) {
	recurse := false
	if fi, err := os.Stat(path); err == nil && fi.IsDir() && hasBookSubfolders(path) {
		recurse = yes("Include subfolders too? [y/N]: ", false)
	}
	files, err := renameCandidates(path, recurse)
	if err != nil {
		fmt.Printf("\n%v\n", err)
		return
	}
	if len(files) == 0 {
		fmt.Printf("\nNo book files in %s\n", path)
		return
	}
	fmt.Printf("\nReading %d book file(s)...\n\n", len(files))

	var todo, skipped []renameItem
	same := 0
	for _, it := range renamePlan(files) {
		switch it.Action {
		case "rename":
			todo = append(todo, it)
		case "skip":
			skipped = append(skipped, it)
		default:
			same++
		}
	}
	for i, it := range append(append([]renameItem{}, todo...), skipped...) {
		shown := it.Current
		if rel, err := filepath.Rel(path, it.Path); err == nil && recurse {
			shown = rel
		}
		fmt.Printf("%2d. %s\n", i+1, shown)
		if it.Action == "skip" {
			fmt.Println("    (skipped)")
		} else {
			fmt.Printf("    -> %s  [%s]\n", it.New, it.Source)
		}
	}
	fmt.Printf("\n%d to rename, %d already correct, %d skipped.\n", len(todo), same, len(skipped))
	if len(skipped) > 0 {
		fmt.Println("Skipped files have no title inside and only symbols in their name.")
	}
	if len(todo) == 0 {
		fmt.Println("Nothing to rename.")
		return
	}
	if !yes(fmt.Sprintf("Rename %d file(s)? [y/N]: ", len(todo)), false) {
		fmt.Println("Nothing renamed.")
		return
	}
	done := 0
	for _, it := range todo {
		if err := applyRename(it); err != nil {
			fmt.Printf("  Failed: %s - %v\n", it.Current, err)
			continue
		}
		done++
		fmt.Printf("  %s\n", it.New)
	}
	fmt.Printf("Renamed %d of %d.\n", done, len(todo))
}
