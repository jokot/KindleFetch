package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Real API responses saved from z-lib.gl.
func TestParseZlibSearch(t *testing.T) {
	body, err := os.ReadFile("testdata/zlib_search_dune.json")
	if err != nil {
		t.Fatal(err)
	}
	books, _, err := parseZlibSearch(body)
	if err != nil {
		t.Fatal(err)
	}

	// Cross-check every record against a plain decode of the same file.
	var raw struct {
		Books []map[string]any `json:"books"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if len(books) != len(raw.Books) || len(books) != 100 {
		t.Fatalf("parsed %d books, file has %d", len(books), len(raw.Books))
	}
	for i, b := range books {
		r := raw.Books[i]
		if b.ZlibHash != r["hash"] || b.MD5 != r["md5"] || b.Ext != r["extension"] {
			t.Errorf("record %d mismatch: %+v", i, b)
		}
		if b.ZlibID == "" || b.Title == "" || b.Source != "zlib" {
			t.Errorf("record %d incomplete: %+v", i, b)
		}
	}
	first := books[0]
	if first.ZlibID != "11735659" || first.Title != "Dune" || first.Year != "2019" {
		t.Errorf("first record: %+v", first)
	}
}

func TestParseZlibSearchUnicode(t *testing.T) {
	body, err := os.ReadFile("testdata/zlib_search_bulgakov.json")
	if err != nil {
		t.Fatal(err)
	}
	books, _, err := parseZlibSearch(body)
	if err != nil || len(books) == 0 {
		t.Fatalf("books=%d err=%v", len(books), err)
	}
	if !strings.Contains(books[0].Title, "Bulgakov") {
		t.Errorf("title: %q", books[0].Title)
	}
}

func TestParseZlibErrors(t *testing.T) {
	if _, _, err := parseZlibSearch([]byte("<html>DiamWall</html>")); stageOf(err) != "challenge" {
		t.Errorf("html page: %v", err)
	}
	books, _, err := parseZlibSearch([]byte(`{"success":1,"books":[],"pagination":{"total_pages":0}}`))
	if err != nil || len(books) != 0 {
		t.Errorf("empty: %v %v", books, err)
	}
}

// Expected values are what the device's shell parser (lgli_parse_results)
// produces for the same file.
func TestParseLibgen(t *testing.T) {
	body, err := os.ReadFile("testdata/libgen_search.html")
	if err != nil {
		t.Fatal(err)
	}
	books := parseLibgen(string(body))
	want := []Book{
		{Source: "lgli", Title: "Dune & the Sands", Author: "Frank Herbert", Publisher: "Ace",
			Year: "1990", Language: "English", Size: "1 MB", Ext: "epub",
			MD5: "0123456789abcdef0123456789abcdef"},
		{Source: "lgli", Title: "Children of Dune", Author: "Herbert, Frank; Smith, J.", Publisher: "Putnam",
			Year: "1976", Language: "Russian", Size: "2 MB", Ext: "fb2",
			MD5: "fedcba9876543210fedcba9876543210"},
	}
	if len(books) != len(want) {
		t.Fatalf("got %d books: %+v", len(books), books)
	}
	for i := range want {
		if books[i] != want[i] {
			t.Errorf("book %d\n got %+v\nwant %+v", i, books[i], want[i])
		}
	}
	if p := libgenLastPage(string(body)); p != 7 {
		t.Errorf("last page %d", p)
	}
}

func TestFilterBooks(t *testing.T) {
	books := []Book{{Ext: "epub", Language: "English"}, {Ext: "pdf", Language: "English"}, {Ext: "epub", Language: "Russian"}}
	cfg := &Config{FormatFilter: "epub", LanguageFilter: "en"}
	if got := filterBooks(books, cfg); len(got) != 1 || got[0].Language != "English" {
		t.Errorf("got %+v", got)
	}

	books = []Book{{Language: "English"}, {Language: "Indonesian"}, {Language: "Russian"}, {Language: "korean"}}
	cfg = &Config{LanguageFilter: "en, id,Korean"}
	if got := filterBooks(books, cfg); len(got) != 3 || got[2].Language != "korean" {
		t.Errorf("several languages: %+v", got)
	}
}

func TestParseLanguages(t *testing.T) {
	langs := []string{"en", "id", "es"}
	cases := map[string]string{
		"1 2":          "en,id",
		"en, id":       "en,id",
		"2,1,2 Korean": "id,en,korean",
		"1 en":         "en",
		"9 3":          "es",
		"1 0":          "",
	}
	for in, want := range cases {
		if got := parseLanguages(in, langs); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
	// A filter saved by 1.1.0 holds a single code and must keep working.
	if got := (&Config{LanguageFilter: "id"}).languages(); len(got) != 1 || got[0] != "id" {
		t.Errorf("old config: %v", got)
	}
}

func TestFileName(t *testing.T) {
	cases := []struct {
		b    Book
		want string
	}{
		{Book{Title: "Dune", Author: "Frank Herbert", Ext: "epub"}, "Dune - Frank Herbert.epub"},
		{Book{Title: `What: a "test"? <yes>/no`, Ext: "PDF"}, "What - a test yes no.pdf"},
		{Book{Title: "Trailing dots...", Ext: "epub"}, "Trailing dots.epub"},
		{Book{Title: " - Dashes - ", Author: "A", Ext: "epub"}, "Dashes - A.epub"},
		{Book{Title: "Man's Search for Meaning", Author: "Viktor E. Frankl; Harold S. Kushner; William J. Winslade", Ext: "epub"},
			"Man's Search for Meaning - Viktor E. Frankl.epub"},
		{Book{Title: "", Ext: ""}, "book.bin"},
	}
	for _, c := range cases {
		if got := c.b.fileName(); got != c.want {
			t.Errorf("%q: got %q want %q", c.b.Title, got, c.want)
		}
	}
	long := Book{Title: strings.Repeat("a", 300), Ext: "epub"}
	if n := len([]rune(long.fileName())); n > 155 {
		t.Errorf("long name not capped: %d", n)
	}
}

func TestUniquePath(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Dune.epub")
	if uniquePath(p) != p {
		t.Fatal("free path changed")
	}
	os.WriteFile(p, []byte("x"), 0o644)
	if got := uniquePath(p); got != filepath.Join(dir, "Dune (2).epub") {
		t.Errorf("got %s", got)
	}
}

func TestKindleCopy(t *testing.T) {
	root := t.TempDir()
	if isKindleRoot(root) {
		t.Fatal("empty dir taken for a Kindle")
	}
	os.MkdirAll(filepath.Join(root, "documents"), 0o755)
	os.MkdirAll(filepath.Join(root, "system"), 0o755)
	if !isKindleRoot(root) {
		t.Fatal("Kindle layout not recognised")
	}

	src := filepath.Join(t.TempDir(), "Dune - Frank Herbert.epub")
	os.WriteFile(src, []byte("book"), 0o644)
	dst, copied, err := copyToKindle(root, src)
	if err != nil || !copied {
		t.Fatalf("copy: %v %v", copied, err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "book" {
		t.Errorf("content %q", b)
	}
	if _, copied, _ := copyToKindle(root, src); copied {
		t.Error("identical book copied twice")
	}
	if _, err := os.Stat(dst + ".part"); err == nil {
		t.Error(".part file left behind")
	}
}

func TestListBooks(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.epub", "b.PDF", "c.part", "notes.md"} {
		os.WriteFile(filepath.Join(dir, f), nil, 0o644)
	}
	if got := listBooks(dir); len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	c := &Config{path: filepath.Join(t.TempDir(), "sub", "config.json"), DownloadDir: "x",
		ZlibCookies: map[string]string{"remix_userid": "1"}}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(c.path)
	var back Config
	if err := json.Unmarshal(b, &back); err != nil || back.ZlibCookies["remix_userid"] != "1" {
		t.Errorf("round trip: %v %+v", err, back)
	}
}

// Live check against the real API. Skipped unless KF_LIVE=1, so the release
// build never depends on a mirror being up.
func TestLiveZlibSearch(t *testing.T) {
	if os.Getenv("KF_LIVE") != "1" {
		t.Skip("set KF_LIVE=1 to run")
	}
	cfg := &Config{ResultsPerPage: 10, FormatFilter: "epub"}
	books, pages, err := zlibSearch(cfg, "dune frank herbert", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d books, %d pages via %s", len(books), pages, cfg.ZlibURL)
	for _, b := range books {
		if b.Ext != "epub" {
			t.Errorf("filter ignored: %+v", b)
		}
	}
	books2, _, err := zlibSearch(cfg, "dune frank herbert", 2)
	if err != nil || books2[0].ZlibID == books[0].ZlibID {
		t.Errorf("page 2 not distinct: %v", err)
	}
	if _, _, err := zlibSearch(cfg, "qzxqzxqzxnotabook", 1); stageOf(err) != "no-match" {
		t.Errorf("no-match: %v", err)
	}
}
