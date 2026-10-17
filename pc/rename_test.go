package main

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// writeTestEpub writes a minimal EPUB: container.xml pointing at
// OEBPS/content.opf with the given title and creators. Each creator is "Name"
// or "Name|role"; epub3 puts roles in <meta refines> instead of opf:role. A
// nil creators slice with an empty title writes no metadata at all.
func writeTestEpub(t *testing.T, path, title string, epub3 bool, creators ...string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	write := func(name, text string) {
		w, _ := zw.Create(name)
		w.Write([]byte(text))
	}
	write("mimetype", "application/epub+zip")
	if title == "" && creators == nil {
		return
	}
	write("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	var meta []string
	if title != "" {
		meta = append(meta, "<dc:title>"+title+"</dc:title>")
	}
	for i, c := range creators {
		name, role, _ := strings.Cut(c, "|")
		switch {
		case epub3:
			meta = append(meta, fmt.Sprintf(`<dc:creator id="c%d">%s</dc:creator>`, i, name))
			if role != "" {
				meta = append(meta, fmt.Sprintf(`<meta refines="#c%d" property="role" scheme="marc:relators">%s</meta>`, i, role))
			}
		case role != "":
			meta = append(meta, fmt.Sprintf(`<dc:creator opf:role="%s">%s</dc:creator>`, role, name))
		default:
			meta = append(meta, "<dc:creator>"+name+"</dc:creator>")
		}
	}
	write("OEBPS/content.opf", `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    `+strings.Join(meta, "\n    ")+`
  </metadata>
</package>`)
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"The Simple Path to Wealth: Your Road Map": "The Simple Path to Wealth - Your Road Map",
		`What? "Quotes" <x>|y*  z`:                 "What Quotes x y z",
		"Book #1 - 100% Done!":                     "Book #1 - 100% Done!",
		" - Title...  ":                            "Title",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestCleanFileStem(t *testing.T) {
	cases := map[string]string{
		"my#weird@@file!!": "my weird file",
		"book_final~v2":    "book final v2",
		"Saint-Exupéry's Prince, Vol. 1 & 2 (x)": "Saint-Exupéry's Prince, Vol. 1 & 2 (x)",
		"Dune (z-library.sk, 1lib.sk, z-lib.sk)": "Dune",
		"Dune (Z-Library)":                       "Dune",
		"###":                                    "",
	}
	for in, want := range cases {
		if got := cleanFileStem(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestEpubMetadata(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m1.epub")
	writeTestEpub(t, p, "Atomic Habits", false, "James Clear")
	if m, ok := epubMetadata(p); !ok || m.Title != "Atomic Habits" || strings.Join(m.Authors, "|") != "James Clear" {
		t.Errorf("basic: %+v %v", m, ok)
	}

	writeTestEpub(t, p, "Book", false, "Ann Author|aut", "Tim Translator|trl", "Bob Writer")
	if m, _ := epubMetadata(p); strings.Join(m.Authors, "|") != "Ann Author|Bob Writer" {
		t.Errorf("EPUB 2 roles: %v", m.Authors)
	}

	writeTestEpub(t, p, "Book", true, "Ann Author|aut", "Ed Editor|edt")
	if m, _ := epubMetadata(p); strings.Join(m.Authors, "|") != "Ann Author" {
		t.Errorf("EPUB 3 roles: %v", m.Authors)
	}

	writeTestEpub(t, p, "", false, "Someone")
	if _, ok := epubMetadata(p); ok {
		t.Error("no title accepted")
	}

	writeTestEpub(t, p, "", false)
	if _, ok := epubMetadata(p); ok {
		t.Error("EPUB without container.xml accepted")
	}

	os.WriteFile(p, []byte("not a zip"), 0o644)
	if _, ok := epubMetadata(p); ok {
		t.Error("non-zip accepted")
	}
}

func TestRenameTarget(t *testing.T) {
	dir := t.TempDir()
	check := func(path, wantName, wantSource string) {
		t.Helper()
		if name, source := renameTarget(path); name != wantName || source != wantSource {
			t.Errorf("%s: got %q [%s] want %q [%s]", filepath.Base(path), name, source, wantName, wantSource)
		}
	}

	p := filepath.Join(dir, "t1.epub")
	writeTestEpub(t, p, "Make It Stick: The Science", false, "Peter Brown", "Henry Roediger")
	check(p, "Make It Stick - The Science - Peter Brown & Henry Roediger.epub", "metadata")

	p = filepath.Join(dir, "t2.epub")
	writeTestEpub(t, p, "The Intelligent Investor", false, "Unknown")
	check(p, "The Intelligent Investor.epub", "metadata")

	p = filepath.Join(dir, "my#weird@@file!!.epub")
	writeTestEpub(t, p, "", false)
	check(p, "my weird file.epub", "cleaned")

	p = filepath.Join(dir, "###.epub")
	writeTestEpub(t, p, "", false)
	check(p, "", "cleaned")

	p = filepath.Join(dir, "Dune (z-library.sk, 1lib.sk, z-lib.sk).PDF")
	os.WriteFile(p, nil, 0o644)
	check(p, "Dune.pdf", "cleaned")
}

func names(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

func TestRenamePlanAndApply(t *testing.T) {
	dir := t.TempDir()
	writeTestEpub(t, filepath.Join(dir, "a.epub"), "Same Book", false, "X")
	writeTestEpub(t, filepath.Join(dir, "b.epub"), "Same Book", false, "X")
	writeTestEpub(t, filepath.Join(dir, "Kept - Y.epub"), "Kept", false, "Y")
	writeTestEpub(t, filepath.Join(dir, "c.epub"), "Taken", false, "Z")
	writeTestEpub(t, filepath.Join(dir, "d.epub"), "Taken", false, "Z")
	os.WriteFile(filepath.Join(dir, "Taken - Z.epub.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "Taken - Z.EPUB"), nil, 0o644) // no metadata: only the extension case changes
	os.Mkdir(filepath.Join(dir, "a.sdr"), 0o755)

	plan := renamePlan(listBooks(dir))
	by := map[string]renameItem{}
	for _, it := range plan {
		by[it.Current] = it
	}
	if by["a.epub"].New != "Same Book - X.epub" || by["b.epub"].New != "Same Book - X (2).epub" {
		t.Errorf("duplicates: %q %q", by["a.epub"].New, by["b.epub"].New)
	}
	if by["Kept - Y.epub"].Action != "same" {
		t.Errorf("correct name not kept: %+v", by["Kept - Y.epub"])
	}
	if by["Taken - Z.EPUB"].New != "Taken - Z.epub" || by["c.epub"].New != "Taken - Z (2).epub" || by["d.epub"].New != "Taken - Z (3).epub" {
		t.Errorf("clashes: %q %q %q", by["Taken - Z.EPUB"].New, by["c.epub"].New, by["d.epub"].New)
	}

	for _, it := range plan {
		if it.Action == "rename" {
			if err := applyRename(it); err != nil {
				t.Errorf("%s: %v", it.Current, err)
			}
		}
	}
	got := names(t, dir)
	for _, want := range []string{"Same Book - X.epub", "Same Book - X (2).epub", "Same Book - X.sdr", "Kept - Y.epub"} {
		if !strings.Contains("|"+got+"|", "|"+want+"|") {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "a.epub") || strings.Contains(got, "a.sdr") {
		t.Errorf("old names left: %s", got)
	}
	if n := len(strings.Split(got, "|")); n != 8 {
		t.Errorf("a file was lost or overwritten: %s", got)
	}
}

func TestRenameCaseOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "animal farm - george orwell.epub")
	writeTestEpub(t, p, "Animal Farm", false, "George Orwell")
	plan := renamePlan([]string{p})
	if plan[0].Action != "rename" {
		t.Fatalf("case change not planned: %+v", plan[0])
	}
	if err := applyRename(plan[0]); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); got != "Animal Farm - George Orwell.epub" {
		t.Errorf("got %s", got)
	}
}

func TestRenameNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.epub")
	writeTestEpub(t, p, "Dune", false)
	plan := renamePlan([]string{p})
	// Another program creates the target between preview and confirm.
	os.WriteFile(filepath.Join(dir, plan[0].New), []byte("keep"), 0o644)
	if err := applyRename(plan[0]); err == nil {
		t.Fatal("overwrote an existing file")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Dune.epub")); string(b) != "keep" {
		t.Error("existing file changed")
	}
}

func TestRenameSkipAndCandidates(t *testing.T) {
	dir := t.TempDir()
	writeTestEpub(t, filepath.Join(dir, "@@.epub"), "", false)
	if plan := renamePlan([]string{filepath.Join(dir, "@@.epub")}); plan[0].Action != "skip" {
		t.Errorf("empty name not skipped: %+v", plan[0])
	}

	os.WriteFile(filepath.Join(dir, "._@@.epub"), nil, 0o644)
	writeTestEpub(t, filepath.Join(dir, "sub", "s.epub"), "S", false)
	os.MkdirAll(filepath.Join(dir, ".Trashes", "t"), 0o755)
	os.WriteFile(filepath.Join(dir, ".Trashes", "t", "gone.epub"), nil, 0o644)

	if !hasBookSubfolders(dir) {
		t.Error("subfolder book not found")
	}
	flat, _ := renameCandidates(dir, false)
	deep, _ := renameCandidates(dir, true)
	if len(flat) != 1 || len(deep) != 2 {
		t.Errorf("flat %v deep %v", flat, deep)
	}
	if _, err := renameCandidates(filepath.Join(dir, "missing"), false); err == nil {
		t.Error("missing path accepted")
	}
}
