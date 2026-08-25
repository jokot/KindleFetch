#!/usr/bin/env bash
rm kindlefetch.zip
rm kindlefetch/bin/kindlefetch_config
rm kindlefetch/bin/zlib_cookies.txt
rm kindlefetch/bin/.version


get_version() {
    api_response=$(curl -s -A "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36" -H "Accept: application/vnd.github.v3+json" "https://api.github.com/repos/joelstitch/KindleFetch/commits") || {
        echo "Warning: Failed to fetch version from GitHub API" >&2
        echo "unknown"
        return
    }

    latest_sha=$(echo "$api_response" | grep -m1 '"sha":' | cut -d'"' -f4 | cut -c1-7)
    
    if [ -n "$latest_sha" ]; then
        echo "${latest_sha}"
    fi
}

VERSION=$(get_version)
mkdir -p "$INSTALL_DIR/bin"
echo "$VERSION" > "kindlefetch/bin/.version"

find kindlefetch -name ".DS_Store" -delete

zip kindlefetch.zip -r kindlefetch