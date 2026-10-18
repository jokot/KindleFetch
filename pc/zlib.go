package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
)

// Z-Library's website is behind DiamWall, but the JSON API under /eapi/ - the
// one the official mobile app uses - answers search without an account on
// some mirrors. Each result carries the id and hash that the file endpoint
// needs, so a download never touches the blocked website either.

// flex accepts a JSON string, number or null, since the API is inconsistent
// about which fields are quoted.
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	if bytes.Equal(b, []byte("null")) {
		*f = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flex(s)
		return nil
	}
	*f = flex(b)
	return nil
}

type zlibBook struct {
	ID        flex `json:"id"`
	Hash      flex `json:"hash"`
	Title     flex `json:"title"`
	Author    flex `json:"author"`
	Extension flex `json:"extension"`
	Size      flex `json:"filesizeString"`
	Year      flex `json:"year"`
	Language  flex `json:"language"`
	Publisher flex `json:"publisher"`
	MD5       flex `json:"md5"`
	Href      flex `json:"href"`
}

type zlibSearchResponse struct {
	Success    int        `json:"success"`
	Error      string     `json:"error"`
	Books      []zlibBook `json:"books"`
	Pagination struct {
		TotalPages int `json:"total_pages"`
	} `json:"pagination"`
}

// The filter menu stores language codes; the API wants English names.
var zlibLanguageNames = map[string]string{
	"en": "english", "ru": "russian", "es": "spanish", "de": "german",
	"fr": "french", "it": "italian", "pt": "portuguese", "nl": "dutch",
	"pl": "polish", "tr": "turkish", "id": "indonesian", "zh": "chinese",
	"ja": "japanese", "ar": "arabic",
}

func zlibLanguage(code string) string {
	if code == "" {
		return ""
	}
	if name, ok := zlibLanguageNames[code]; ok {
		return name
	}
	return strings.ToLower(code)
}

func parseZlibSearch(body []byte) ([]Book, int, error) {
	var r zlibSearchResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, 0, &failStage{"challenge", "not an API response"}
	}
	if r.Success != 1 {
		return nil, 0, &failStage{"empty", r.Error}
	}
	books := make([]Book, 0, len(r.Books))
	for _, b := range r.Books {
		if b.ID == "" || b.Hash == "" || b.Title == "" {
			continue
		}
		year := string(b.Year)
		if year == "0" {
			year = ""
		}
		books = append(books, Book{
			Source: "zlib", Title: strings.TrimSpace(string(b.Title)),
			Author: strings.TrimSpace(string(b.Author)), Ext: string(b.Extension),
			Size: string(b.Size), Year: year, Language: string(b.Language),
			Publisher: string(b.Publisher), MD5: string(b.MD5),
			ZlibID: string(b.ID), ZlibHash: string(b.Hash), URL: string(b.Href),
		})
	}
	return books, r.Pagination.TotalPages, nil
}

// zlibSearch walks the mirrors and returns the first page of real results.
// On success cfg.ZlibURL is set to the mirror that answered.
func zlibSearch(cfg *Config, query string, page int) ([]Book, int, error) {
	return zlibSearchOn(cfg, mirrors(cfg.ZlibURL, defaultZlibMirrors), query, page)
}

func zlibSearchOn(cfg *Config, candidates []string, query string, page int) ([]Book, int, error) {
	form := url.Values{}
	form.Set("message", query)
	form.Set("limit", strconv.Itoa(cfg.ResultsPerPage))
	form.Set("page", strconv.Itoa(page))
	if cfg.FormatFilter != "" {
		form.Add("extensions[]", cfg.FormatFilter)
	}
	for _, l := range cfg.languages() {
		form.Add("languages[]", zlibLanguage(l))
	}

	var last error = &failStage{Stage: "empty"}
	for _, m := range candidates {
		req, _ := http.NewRequest(http.MethodPost, m+"/eapi/book/search", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		body, err := fetch(req)
		if err != nil {
			last = err
			continue
		}
		books, pages, err := parseZlibSearch(body)
		if err != nil {
			last = err
			continue
		}
		if len(books) == 0 {
			// A genuine answer with no matches. Every mirror serves the same
			// catalogue, so asking another would only cost time.
			return nil, 0, &failStage{Stage: "no-match"}
		}
		cfg.ZlibURL = m
		return books, pages, nil
	}
	return nil, 0, last
}

func (c *Config) zlibLoggedIn() bool {
	return len(c.ZlibCookies) > 0
}

func (c *Config) zlibCookieHeader() string {
	parts := []string{}
	for k, v := range c.ZlibCookies {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

// zlibLogin signs in through the API and keeps the session cookies. The
// cookies are sent explicitly on later requests, so they work on whichever
// mirror search settles on.
func zlibLogin(cfg *Config, email, password string) error {
	form := url.Values{"email": {email}, "password": {password}}
	var last error = &failStage{Stage: "network"}
	for _, m := range mirrors(cfg.ZlibURL, defaultZlibMirrors) {
		jar, _ := cookiejar.New(nil)
		client := *apiClient
		client.Jar = jar
		req, _ := http.NewRequest(http.MethodPost, m+"/eapi/user/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		body, err := fetchWith(&client, req)

		var r struct {
			Success int    `json:"success"`
			Error   string `json:"error"`
			User    struct {
				ID      flex `json:"id"`
				Name    flex `json:"name"`
				UserKey flex `json:"remix_userkey"`
			} `json:"user"`
		}
		if jerr := json.Unmarshal(body, &r); jerr != nil {
			if err == nil {
				err = &failStage{"challenge", "not an API response"}
			}
			last = err
			continue
		}
		if r.Success != 1 {
			// A real answer, so the credentials themselves were refused.
			msg := r.Error
			if msg == "" {
				msg = "login refused"
			}
			return errors.New(msg)
		}

		cookies := map[string]string{}
		u, _ := url.Parse(m)
		for _, ck := range jar.Cookies(u) {
			cookies[ck.Name] = ck.Value
		}
		if r.User.ID != "" && r.User.UserKey != "" {
			cookies["remix_userid"] = string(r.User.ID)
			cookies["remix_userkey"] = string(r.User.UserKey)
		}
		if len(cookies) == 0 {
			return errors.New("login succeeded but no session was returned")
		}
		cfg.ZlibCookies = cookies
		cfg.ZlibUser = string(r.User.Name)
		if cfg.ZlibUser == "" {
			cfg.ZlibUser = email
		}
		cfg.ZlibURL = m
		return nil
	}
	return last
}

// zlibFileLink asks the API for a book's direct download link.
func zlibFileLink(cfg *Config, b Book) (string, error) {
	if !cfg.zlibLoggedIn() {
		return "", errors.New("Z-Library downloads need a login (Settings > Z-Library login)")
	}
	var last error
	for _, m := range mirrors(cfg.ZlibURL, defaultZlibMirrors) {
		req, _ := http.NewRequest(http.MethodGet,
			fmt.Sprintf("%s/eapi/book/%s/%s/file", m, b.ZlibID, b.ZlibHash), nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Cookie", cfg.zlibCookieHeader())
		body, err := fetch(req)

		var r struct {
			Success int    `json:"success"`
			Error   string `json:"error"`
			File    struct {
				DownloadLink string `json:"downloadLink"`
			} `json:"file"`
		}
		if jerr := json.Unmarshal(body, &r); jerr != nil {
			if err == nil {
				err = &failStage{"challenge", "not an API response"}
			}
			last = err
			continue
		}
		// An expired login or a spent daily limit comes back as a JSON error.
		// That is the account, not the mirror, so stop and report it.
		if r.Success != 1 || r.File.DownloadLink == "" {
			msg := r.Error
			if msg == "" {
				msg = "no download link in the response"
			}
			return "", errors.New("Z-Library: " + msg)
		}
		return r.File.DownloadLink, nil
	}
	return "", last
}
