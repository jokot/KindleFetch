// KindleFetch for Windows and macOS.
//
// A terminal port of KindleFetch (https://github.com/justrals/KindleFetch):
// search, download to this computer, and copy to a Kindle plugged in by USB.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Set at build time with -ldflags "-X main.version=...".
var version = "dev"

const repo = "jokot/KindleFetch"

var in = bufio.NewReader(os.Stdin)

func prompt(label string) string {
	fmt.Print(label)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		// Stdin closed: there is no one left to answer, so leave cleanly.
		fmt.Println()
		os.Exit(0)
	}
	return strings.TrimSpace(line)
}

func pause() {
	prompt("\nPress Enter to continue...")
}

func yes(label string, def bool) bool {
	a := strings.ToLower(prompt(label))
	if a == "" {
		return def
	}
	return a == "y" || a == "yes"
}

func clearScreen() {
	if runtime.GOOS == "windows" {
		// Older consoles ignore ANSI escapes unless VT mode is enabled.
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		_ = cmd.Run()
		return
	}
	fmt.Print("\033[H\033[2J\033[3J")
}

const banner = ` _  ___           _ _      ______   _       _
| |/ (_)         | | |    |  ____| | |     | |
| ' / _ _ __   __| | | ___| |__ ___| |_ ___| |__
|  < | | '_ \ / _` + "`" + ` | |/ _ \  __/ _ \ __/ __| '_ \
| . \| | | | | (_| | |  __/ | |  __/ || (__| | | |
|_|\_\_|_| |_|\__,_|_|\___|_|  \___|\__\___|_| |_|`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Println("KindleFetch for PC", version)
			return
		case "--diagnostics":
			runDiagnostics(loadConfig())
			return
		case "--rename":
			// kindlefetch --rename [folder or file]; the download folder by default.
			path := strings.Join(os.Args[2:], " ")
			if path == "" {
				path = loadConfig().DownloadDir
			}
			renameFlow(cleanDroppedPath(path))
			return
		}
	}

	cfg := loadConfig()
	update := latestRelease()

	for {
		clearScreen()
		fmt.Println(banner)
		fmt.Printf("\nKindleFetch for PC %s | https://github.com/%s\n\n", version, repo)
		if update != "" {
			fmt.Printf("Update available: %s. Run the install command again to update.\n\n", update)
		}
		if k := findKindle(); k != "" {
			fmt.Printf("Kindle connected at %s\n\n", k)
		}
		fmt.Println("1. Search and download books")
		fmt.Println("2. Filter search results" + filterSummary(cfg))
		fmt.Println("3. List my books")
		fmt.Println("4. Send books to Kindle (USB)")
		fmt.Println("5. Rename book files")
		fmt.Println("6. Settings")
		fmt.Println("q. Exit")
		fmt.Println()

		switch strings.ToLower(prompt("Choose option: ")) {
		case "1":
			searchMenu(cfg)
		case "2":
			filtersMenu(cfg)
		case "3":
			listMenu(cfg)
		case "4":
			sendAllMenu(cfg)
		case "5":
			renameMenu(cfg)
		case "6":
			settingsMenu(cfg)
		case "q":
			return
		}
	}
}

func filterSummary(cfg *Config) string {
	var f []string
	if cfg.FormatFilter != "" {
		f = append(f, cfg.FormatFilter)
	}
	if cfg.LanguageFilter != "" {
		f = append(f, cfg.LanguageFilter)
	}
	if len(f) == 0 {
		return ""
	}
	return " [" + strings.Join(f, ", ") + "]"
}

// searchFailure explains why a search produced nothing.
func searchFailure(query string, err error) {
	fmt.Println()
	switch s := stageOf(err); {
	case s == "no-match":
		fmt.Printf("No results for '%s'.\n", query)
		fmt.Println("Try fewer words, or clear the format and language filters.")
	case s == "dns":
		fmt.Println("Search failed: could not resolve any mirror. Check the internet connection.")
	case s == "tls":
		fmt.Println("Search failed: the secure connection was rejected.")
	case s == "timeout":
		fmt.Println("Search failed: every mirror timed out.")
	case s == "challenge":
		fmt.Println("Search failed: the mirrors answered with a bot-protection challenge.")
	case strings.HasPrefix(s, "http-"):
		fmt.Printf("Search failed: mirror returned HTTP %s.\n", strings.TrimPrefix(s, "http-"))
	default:
		fmt.Printf("Search failed: %v\n", err)
	}
	pause()
}

// search runs one page against Z-Library, falling back to Libgen.
func search(cfg *Config, query string, page int, source string) ([]Book, int, string, error) {
	if source == "" || source == "zlib" {
		books, pages, err := zlibSearch(cfg, query, page)
		if err == nil {
			_ = cfg.save()
			return books, pages, "zlib", nil
		}
		// Z-Library answered and has nothing; its catalogue already covers
		// Libgen's, so falling back would only add Libgen's timeouts.
		if source == "zlib" || stageOf(err) == "no-match" {
			return nil, 0, "", err
		}
		fmt.Println("Z-Library returned nothing; trying Libgen...")
	}
	books, pages, err := libgenSearch(cfg, query, page)
	if err == nil {
		_ = cfg.save()
		return books, pages, "lgli", nil
	}
	return nil, 0, "", err
}

func searchMenu(cfg *Config) {
	query := prompt("Enter search query: ")
	if query == "" {
		return
	}
	page, source := 1, ""
	for {
		fmt.Printf("Searching for '%s' (page %d)...\n", query, page)
		books, pages, src, err := search(cfg, query, page, source)
		if err != nil {
			searchFailure(query, err)
			return
		}
		// Stay on one backend while paging, so page 2 continues page 1.
		source = src
		if pages < page {
			pages = page
		}

		for {
			clearScreen()
			fmt.Printf("Results for '%s'  (%s)\n\n", query, map[string]string{"zlib": "Z-Library", "lgli": "Libgen"}[src])
			for i := len(books) - 1; i >= 0; i-- {
				b := books[i]
				fmt.Printf("%2d. %s\n", i+1, b.Title)
				if b.Author != "" {
					fmt.Printf("    by %s\n", b.Author)
				}
				fmt.Printf("    %s\n\n", b.Summary())
			}
			fmt.Printf("Page %d of %d\n\n", page, pages)
			if page > 1 {
				fmt.Print("p: Previous page | ")
			}
			if page < pages {
				fmt.Print("n: Next page | ")
			}
			fmt.Printf("t[1-%d]: Go to page | 1-%d: Download | q: Back\n\n", pages, len(books))

			choice := strings.ToLower(prompt("Enter choice: "))
			switch {
			case choice == "q":
				return
			case choice == "n" && page < pages:
				page++
			case choice == "p" && page > 1:
				page--
			case strings.HasPrefix(choice, "t"):
				n, err := strconv.Atoi(choice[1:])
				if err != nil || n < 1 || n > pages || n == page {
					continue
				}
				page = n
			default:
				n, err := strconv.Atoi(choice)
				if err == nil && n >= 1 && n <= len(books) {
					downloadMenu(cfg, books[n-1])
				}
				continue
			}
			break // page changed: fetch it
		}
	}
}

func downloadMenu(cfg *Config, b Book) {
	fmt.Printf("\n%s\n", b.Title)
	sources := []string{b.Source}
	if b.Source == "zlib" && b.MD5 != "" {
		// Libgen can serve the same file by md5 when it is up - useful once
		// the daily Z-Library download limit is spent.
		sources = append(sources, "lgli")
	}
	names := map[string]string{"zlib": "Z-Library", "lgli": "Libgen"}
	for i, s := range sources {
		note := ""
		if s == "zlib" && !cfg.zlibLoggedIn() {
			note = " (login required)"
		}
		fmt.Printf("%d. %s%s\n", i+1, names[s], note)
	}
	fmt.Println("c. Cancel")

	n, err := strconv.Atoi(prompt("Choose source: "))
	if err != nil || n < 1 || n > len(sources) {
		return
	}
	src := sources[n-1]
	if src == "zlib" && !cfg.zlibLoggedIn() {
		if !loginFlow(cfg) {
			pause()
			return
		}
	}

	path, err := downloadBook(cfg, b, src, cfg.DownloadDir)
	if err != nil {
		fmt.Printf("\nDownload failed: %v\n", err)
		pause()
		return
	}
	fmt.Printf("Saved to: %s\n", path)
	offerKindleCopy(cfg, []string{path})
	pause()
}

func offerKindleCopy(cfg *Config, paths []string) {
	if cfg.CopyToKindle == "never" {
		return
	}
	root := findKindle()
	if root == "" {
		return
	}
	if cfg.CopyToKindle == "ask" && !yes(fmt.Sprintf("\nKindle found at %s. Copy to it? [Y/n]: ", root), true) {
		return
	}
	for _, p := range paths {
		dst, copied, err := copyToKindle(root, p)
		switch {
		case err != nil:
			fmt.Printf("  Could not copy %s: %v\n", filepath.Base(p), err)
		case copied:
			fmt.Printf("  Copied to %s\n", dst)
		default:
			fmt.Printf("  Already on Kindle: %s\n", filepath.Base(p))
		}
	}
	fmt.Println("Eject the Kindle safely before unplugging it so it picks the books up.")
}

func listMenu(cfg *Config) {
	clearScreen()
	books := listBooks(cfg.DownloadDir)
	fmt.Printf("Books in %s\n\n", cfg.DownloadDir)
	if len(books) == 0 {
		fmt.Println("(none yet)")
	}
	for i, p := range books {
		size := ""
		if fi, err := os.Stat(p); err == nil {
			size = humanSize(fi.Size())
		}
		fmt.Printf("%2d. %s  (%s)\n", i+1, filepath.Base(p), size)
	}
	fmt.Println("\no. Open the folder | Enter: Back")
	if strings.ToLower(prompt("\nChoose: ")) == "o" {
		openFolder(cfg.DownloadDir)
	}
}

func openFolder(dir string) {
	_ = os.MkdirAll(dir, 0o755)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", dir)
	case "darwin":
		cmd = exec.Command("open", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	_ = cmd.Start()
}

func sendAllMenu(cfg *Config) {
	clearScreen()
	root := findKindle()
	if root == "" {
		fmt.Println("No Kindle found.")
		fmt.Println()
		fmt.Println("Plug it in with a USB cable and unlock it. It should appear as a drive")
		fmt.Println("(Windows) or under /Volumes (Mac) with a 'documents' folder.")
		fmt.Println("Newer Kindles that connect over MTP do not appear as a drive and are not")
		fmt.Println("supported; use Amazon's Send to Kindle for those.")
		pause()
		return
	}
	books := listBooks(cfg.DownloadDir)
	if len(books) == 0 {
		fmt.Printf("No books in %s yet.\n", cfg.DownloadDir)
		pause()
		return
	}
	fmt.Printf("Kindle found at %s\n\n", root)
	for i, p := range books {
		fmt.Printf("%2d. %s\n", i+1, filepath.Base(p))
	}
	choice := strings.ToLower(prompt("\nCopy which? (a = all, numbers like 1 3 5, Enter = cancel): "))
	var pick []string
	if choice == "a" {
		pick = books
	} else {
		for _, f := range strings.FieldsFunc(choice, func(r rune) bool { return r == ' ' || r == ',' }) {
			if n, err := strconv.Atoi(f); err == nil && n >= 1 && n <= len(books) {
				pick = append(pick, books[n-1])
			}
		}
	}
	if len(pick) == 0 {
		return
	}
	saved := cfg.CopyToKindle
	cfg.CopyToKindle = "always"
	offerKindleCopy(cfg, pick)
	cfg.CopyToKindle = saved
	pause()
}

func filtersMenu(cfg *Config) {
	formats := []string{"epub", "azw3", "mobi", "pdf", "fb2", "txt", "djvu", "cbz", "cbr"}
	langs := []string{"en", "id", "es", "fr", "de", "ru", "pt", "it", "ja", "zh"}
	for {
		clearScreen()
		fmt.Println("Filters")
		fmt.Println()
		fmt.Printf("1. Format:   %s\n", orAny(cfg.FormatFilter))
		fmt.Printf("2. Language: %s\n", orAny(cfg.LanguageFilter))
		fmt.Println("3. Clear all filters")
		fmt.Println("q. Back")
		switch strings.ToLower(prompt("\nChoose option: ")) {
		case "1":
			cfg.FormatFilter = pickFrom("Format", formats, false)
		case "2":
			cfg.LanguageFilter = pickFrom("Language", langs, true)
		case "3":
			cfg.FormatFilter, cfg.LanguageFilter = "", ""
		case "q", "":
			_ = cfg.save()
			return
		}
		_ = cfg.save()
	}
}

func orAny(s string) string {
	if s == "" {
		return "any"
	}
	return s
}

func pickFrom(label string, options []string, custom bool) string {
	fmt.Println()
	for i, o := range options {
		fmt.Printf("%d) %s  ", i+1, o)
	}
	fmt.Println("\n0) Any")
	hint := ""
	if custom {
		hint = " (or type a language name/code)"
	}
	a := strings.ToLower(prompt(label + hint + ": "))
	if n, err := strconv.Atoi(a); err == nil {
		if n >= 1 && n <= len(options) {
			return options[n-1]
		}
		return ""
	}
	if custom {
		return a
	}
	return ""
}

func loginFlow(cfg *Config) bool {
	fmt.Println()
	email := prompt("Z-Library email: ")
	if email == "" {
		return false
	}
	password := readPassword("Z-Library password: ")
	fmt.Println("Logging in...")
	if err := zlibLogin(cfg, email, password); err != nil {
		fmt.Printf("Login failed: %v\n", err)
		return false
	}
	_ = cfg.save()
	fmt.Printf("Logged in as %s.\n", cfg.ZlibUser)
	return true
}

func settingsMenu(cfg *Config) {
	for {
		clearScreen()
		fmt.Println("Settings")
		fmt.Println()
		fmt.Printf("1. Download folder:   %s\n", cfg.DownloadDir)
		if cfg.zlibLoggedIn() {
			fmt.Printf("2. Z-Library login:   logged in as %s (choose to log out)\n", cfg.ZlibUser)
		} else {
			fmt.Println("2. Z-Library login:   not logged in")
		}
		fmt.Printf("3. Copy to Kindle:    %s\n", cfg.CopyToKindle)
		fmt.Printf("4. Results per page:  %d\n", cfg.ResultsPerPage)
		fmt.Printf("5. Mirrors:           Z-Library %s | Libgen %s\n", orAny(cfg.ZlibURL), orAny(cfg.LibgenURL))
		fmt.Println("6. Network diagnostics")
		fmt.Printf("\nConfig file: %s\n", cfg.path)
		fmt.Println("q. Back")

		switch strings.ToLower(prompt("\nChoose option: ")) {
		case "1":
			if d := prompt("New download folder (Enter = keep): "); d != "" {
				cfg.DownloadDir = d
			}
		case "2":
			if cfg.zlibLoggedIn() {
				if yes("Log out of Z-Library? [y/N]: ", false) {
					cfg.ZlibCookies, cfg.ZlibUser = nil, ""
				}
			} else {
				loginFlow(cfg)
				pause()
			}
		case "3":
			cfg.CopyToKindle = map[string]string{"ask": "always", "always": "never", "never": "ask"}[cfg.CopyToKindle]
		case "4":
			if n, err := strconv.Atoi(prompt("Results per page (1-50): ")); err == nil && n >= 1 && n <= 50 {
				cfg.ResultsPerPage = n
			}
		case "5":
			fmt.Println("Leave empty to pick automatically.")
			cfg.ZlibURL = strings.TrimRight(prompt("Z-Library mirror (e.g. https://z-lib.gl): "), "/")
			cfg.LibgenURL = strings.TrimRight(prompt("Libgen mirror (e.g. https://libgen.la): "), "/")
		case "6":
			runDiagnostics(cfg)
			pause()
		case "q", "":
			_ = cfg.save()
			return
		}
		_ = cfg.save()
	}
}

func runDiagnostics(cfg *Config) {
	fmt.Println("\nNetwork diagnostics")
	fmt.Println("===================")
	fmt.Printf("KindleFetch for PC %s, %s/%s\n\n", version, runtime.GOOS, runtime.GOARCH)

	probe := func(name string, books int, err error) {
		if err == nil {
			fmt.Printf("  %-20s OK - %d results\n", name, books)
		} else {
			fmt.Printf("  %-20s FAILED - %s\n", name, stageOf(err))
		}
	}
	saved := *cfg
	cfg.FormatFilter, cfg.LanguageFilter = "", ""
	fmt.Println("Z-Library (search API):")
	for _, m := range defaultZlibMirrors {
		b, _, err := zlibSearchOn(cfg, []string{m}, "bible", 1)
		probe(strings.TrimPrefix(m, "https://"), len(b), err)
	}
	fmt.Println("Libgen:")
	for _, m := range defaultLibgenMirrors {
		body, err := get(m + "/index.php?req=bible")
		n := 0
		if err == nil {
			n = len(parseLibgen(string(body)))
			if n == 0 {
				err = &failStage{Stage: "empty"}
				if isJunk(body) {
					err = &failStage{Stage: "challenge"}
				}
			}
		}
		probe(strings.TrimPrefix(m, "https://"), n, err)
	}
	*cfg = saved
	if k := findKindle(); k != "" {
		fmt.Printf("\nKindle:    connected at %s\n", k)
	} else {
		fmt.Println("\nKindle:    not connected")
	}
}

// latestRelease returns the newest PC release tag when it differs from this
// build, or "" when up to date, offline, or a development build.
func latestRelease() string {
	if version == "dev" {
		return ""
	}
	body, err := get("https://api.github.com/repos/" + repo + "/releases?per_page=20")
	if err != nil {
		return ""
	}
	var rels []struct {
		Tag   string `json:"tag_name"`
		Draft bool   `json:"draft"`
		Pre   bool   `json:"prerelease"`
	}
	if json.Unmarshal(body, &rels) != nil {
		return ""
	}
	for _, r := range rels {
		if !r.Draft && !r.Pre && strings.HasPrefix(r.Tag, "pc-v") {
			if r.Tag != version {
				return r.Tag
			}
			return ""
		}
	}
	return ""
}
