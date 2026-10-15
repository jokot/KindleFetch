package main

import (
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Library Genesis serves plain HTML that needs no challenge, but answers
// inconsistently: a mirror may return 200 with an nginx default page or with
// a results table holding no results. Mirrors are judged by content.

var (
	reLgMD5    = regexp.MustCompile(`ads\.php\?md5=([0-9a-fA-F]{32})`)
	reLgTitle  = regexp.MustCompile(`edition\.php\?id=[0-9]+">([^<]*)`)
	reLgPage   = regexp.MustCompile(`page=([0-9]+)`)
	reLgGet    = regexp.MustCompile(`href="([^"]*get\.php[^"]*)"`)
	reTag      = regexp.MustCompile(`<[^>]*>`)
	reSpaces   = regexp.MustCompile(`\s+`)
	reOpenRest = regexp.MustCompile(`^[^>]*>`)
)

// cleanCell strips the rest of an opening <td ...> tag and all markup.
func cleanCell(s string) string {
	s = reOpenRest.ReplaceAllString(s, "")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
}

// parseLibgen reads a libgen.li-style results table. Cells are located
// relative to the mirrors cell, whose trailing neighbours are fixed, rather
// than from the left, where the title cell varies with series markup.
func parseLibgen(page string) []Book {
	var books []Book
	for _, row := range strings.Split(page, "<tr")[1:] {
		m := reLgMD5.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		cells := strings.Split(row, "<td")
		mcell := -1
		for k := 1; k < len(cells); k++ {
			if strings.Contains(cells[k], "ads.php?md5=") {
				mcell = k
				break
			}
		}
		if mcell < 2 {
			continue
		}
		at := func(off int) string {
			if mcell-off >= 1 {
				return cleanCell(cells[mcell-off])
			}
			return ""
		}

		title := ""
		if t := reLgTitle.FindStringSubmatch(row); t != nil {
			title = strings.TrimSpace(reSpaces.ReplaceAllString(html.UnescapeString(t[1]), " "))
		}
		if title == "" {
			title = cleanCell(cells[1])
		}
		if title == "" {
			continue
		}
		books = append(books, Book{
			Source: "lgli", Title: title, MD5: strings.ToLower(m[1]),
			Author: at(7), Publisher: at(6), Year: at(5), Language: at(4),
			Size: at(2), Ext: at(1),
		})
	}
	return books
}

func libgenLastPage(page string) int {
	last := 1
	for _, m := range reLgPage.FindAllStringSubmatch(page, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > last {
			last = n
		}
	}
	return last
}

// libgenSearch tries each mirror twice, since a wrong answer is usually an
// immediate 200 rather than a timeout. Filters are applied to the parsed
// rows. On success cfg.LibgenURL is set to the mirror that answered.
func libgenSearch(cfg *Config, query string, page int) ([]Book, int, error) {
	q := url.Values{"req": {query}, "page": {strconv.Itoa(page)}}
	var last error = &failStage{Stage: "empty"}
	attempts := 0
	for _, m := range mirrors(cfg.LibgenURL, defaultLibgenMirrors) {
		for try := 0; try < 2 && attempts < 8; try++ {
			attempts++
			body, err := get(m + "/index.php?" + q.Encode())
			if err != nil {
				last = err
				if stageOf(err) == "dns" || stageOf(err) == "timeout" {
					break // retrying a dead host only adds waiting
				}
				continue
			}
			if !strings.Contains(string(body), "ads.php?md5=") {
				if isJunk(body) {
					last = &failStage{Stage: "challenge"}
				} else {
					last = &failStage{Stage: "empty"}
				}
				continue
			}
			cfg.LibgenURL = m
			books := filterBooks(parseLibgen(string(body)), cfg)
			if len(books) == 0 {
				return nil, 0, &failStage{Stage: "no-match"}
			}
			return books, libgenLastPage(string(body)), nil
		}
	}
	return nil, 0, last
}

func filterBooks(books []Book, cfg *Config) []Book {
	out := books[:0]
	lang := zlibLanguage(cfg.LanguageFilter)
	for _, b := range books {
		if cfg.FormatFilter != "" && !strings.EqualFold(b.Ext, cfg.FormatFilter) {
			continue
		}
		if lang != "" && !strings.EqualFold(b.Language, lang) {
			continue
		}
		out = append(out, b)
	}
	return out
}

// libgenFileLink resolves a book's get.php link from its ads.php page. The
// link may be absolute, root-relative or bare.
func libgenFileLink(cfg *Config, b Book) (string, error) {
	var last error = &failStage{Stage: "empty"}
	for _, m := range mirrors(cfg.LibgenURL, defaultLibgenMirrors) {
		pageURL := m + "/ads.php?md5=" + b.MD5
		body, err := get(pageURL)
		if err != nil {
			last = err
			continue
		}
		link := reLgGet.FindStringSubmatch(string(body))
		if link == nil {
			continue
		}
		base, _ := url.Parse(pageURL)
		ref, err := url.Parse(html.UnescapeString(link[1]))
		if err != nil {
			continue
		}
		return base.ResolveReference(ref).String(), nil
	}
	return "", last
}
