package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

var defaultZlibMirrors = []string{
	// Mirrors whose app API answered search without a challenge, first.
	"https://z-lib.gl", "https://z-lib.gd", "https://library-access.sk",
	"https://z-library.sk", "https://1lib.sk",
}

var defaultLibgenMirrors = []string{
	"https://libgen.la", "https://libgen.gl", "https://libgen.vg",
	"https://libgen.bz", "https://libgen.li",
}

type Config struct {
	DownloadDir    string `json:"download_dir"`
	ResultsPerPage int    `json:"results_per_page"`

	// "ask", "always" or "never": what to do with a finished download when
	// a Kindle is plugged in.
	CopyToKindle string `json:"copy_to_kindle"`

	ZlibURL     string            `json:"zlib_url"`
	ZlibUser    string            `json:"zlib_user,omitempty"`
	ZlibCookies map[string]string `json:"zlib_cookies,omitempty"`
	LibgenURL   string            `json:"libgen_url"`

	FormatFilter string `json:"format_filter,omitempty"`
	// Language codes or names separated by commas, e.g. "en,id".
	LanguageFilter string `json:"language_filter,omitempty"`

	path string
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir, _ = os.UserHomeDir()
	}
	return filepath.Join(dir, "KindleFetch", "config.json")
}

func defaultDownloadDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "KindleFetch"
	}
	return filepath.Join(home, "Downloads", "KindleFetch")
}

func loadConfig() *Config {
	c := &Config{path: configPath()}
	if b, err := os.ReadFile(c.path); err == nil {
		_ = json.Unmarshal(b, c)
	}
	if c.DownloadDir == "" {
		c.DownloadDir = defaultDownloadDir()
	}
	if c.ResultsPerPage < 1 || c.ResultsPerPage > 50 {
		c.ResultsPerPage = 10
	}
	switch c.CopyToKindle {
	case "ask", "always", "never":
	default:
		c.CopyToKindle = "ask"
	}
	return c
}

// save writes the config readable only by this user, since it holds the
// Z-Library login cookies.
func (c *Config) save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o600)
}

// languages returns the language filter as a list, without duplicates.
func (c *Config) languages() []string {
	return splitList(c.LanguageFilter)
}

// splitList splits on commas and spaces, lower-cases and drops duplicates.
func splitList(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// mirrors returns the preferred mirror followed by the rest of the defaults.
func mirrors(preferred string, defaults []string) []string {
	out := []string{}
	if preferred != "" {
		out = append(out, preferred)
	}
	for _, m := range defaults {
		if m != preferred {
			out = append(out, m)
		}
	}
	return out
}
