package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Several mirrors reject Go's default User-Agent outright, so every request
// shares this one.
const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36"

// failStage names why a request produced nothing usable, so a failed search
// can say something true instead of printing an empty list.
type failStage struct {
	Stage  string // dns, tls, timeout, network, http-NNN, challenge, empty, no-match
	Detail string
}

func (f *failStage) Error() string {
	if f.Detail != "" {
		return f.Stage + ": " + f.Detail
	}
	return f.Stage
}

func stageOf(err error) string {
	var fs *failStage
	if errors.As(err, &fs) {
		return fs.Stage
	}
	return "network"
}

// apiClient is for API calls and page fetches. Z-Library's website answers
// with a DiamWall 307 loop, so redirects are capped to make blocked mirrors
// fail in milliseconds rather than after fifty hops.
var apiClient = &http.Client{
	Timeout: 25 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

// downloadClient has no overall timeout - a large book on a slow link can
// legitimately take minutes - but still gives up on a server that stalls.
var downloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 60 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// classify turns a transport error into a stage.
func classify(err error) *failStage {
	msg := err.Error()
	var dnsErr *net.DNSError
	var urlErr *url.Error
	switch {
	case errors.As(err, &dnsErr):
		return &failStage{"dns", msg}
	case errors.As(err, &urlErr) && urlErr.Timeout():
		return &failStage{"timeout", msg}
	case strings.Contains(msg, "certificate"), strings.Contains(msg, "tls:"):
		return &failStage{"tls", msg}
	}
	return &failStage{"network", msg}
}

// fetch performs req and returns the body when the status is 2xx. Anything
// else becomes a *failStage, with the body still returned so callers can read
// an API's own error message from a 4xx.
func fetch(req *http.Request) ([]byte, error) {
	return fetchWith(apiClient, req)
}

func fetchWith(client *http.Client, req *http.Request) ([]byte, error) {
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, classify(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, classify(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return body, &failStage{Stage: fmt.Sprintf("http-%d", resp.StatusCode)}
	}
	return body, nil
}

func get(rawURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &failStage{"network", err.Error()}
	}
	return fetch(req)
}

// isJunk reports whether a body is an anti-bot interstitial or placeholder
// rather than real content.
func isJunk(body []byte) bool {
	s := string(body)
	for _, marker := range []string{
		"DDoS-Guard", "ddos-guard", "DiamWall", "__diamwall",
		"Just a moment", "_cf_chl", "Checking your browser", "Welcome to nginx",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// progressWriter prints download progress on one line.
type progressWriter struct {
	total, done int64
	last        time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.last) > 200*time.Millisecond {
		p.print()
		p.last = time.Now()
	}
	return len(b), nil
}

func (p *progressWriter) print() {
	if p.total > 0 {
		fmt.Printf("\r  %s / %s (%d%%)   ", humanSize(p.done), humanSize(p.total), p.done*100/p.total)
	} else {
		fmt.Printf("\r  %s   ", humanSize(p.done))
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// downloadTo streams rawURL into path, writing to a .part file first so an
// interrupted download never leaves something that looks like a finished book.
func downloadTo(rawURL, path string, header http.Header) error {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := downloadClient.Do(req)
	if err != nil {
		return classify(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &failStage{Stage: fmt.Sprintf("http-%d", resp.StatusCode)}
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		// A file server answering with a web page means a challenge or an
		// error page, not the book.
		return &failStage{"challenge", "server returned a web page instead of the file"}
	}

	part := path + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	pw := &progressWriter{total: resp.ContentLength}
	_, err = io.Copy(io.MultiWriter(f, pw), resp.Body)
	pw.print()
	fmt.Println()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return classify(err)
	}
	return os.Rename(part, path)
}
