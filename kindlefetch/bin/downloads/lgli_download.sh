#!/bin/sh

lgli_download() {
    local index="$1"
    
    if [ ! -f "$TMP_DIR/search_results.json" ]; then
        echo "No search results found" >&2
        return 1
    fi
    
    local book_info="$(awk -v i="$index" 'BEGIN{RS="\\{"; FS="\\}"} NR==i+1{print $1}' "$TMP_DIR"/search_results.json)"
    if [ -z "$book_info" ]; then
        echo "Invalid book selection"
        return 1
    fi
    
    local md5="$(get_json_value "$book_info" "md5")"
    local title="$(get_json_value "$book_info" "title")"
    local format="$(get_json_value "$book_info" "format")"
    
    printf "\nDownloading: $title"

    local clean_title="$(sanitize_filename "$title" | tr -d ' ')"

    printf '\nDo you want to change filename? [y/N]: '
    read -r confirm
    if [ "$confirm" = "y" ] || [ "$confirm" = "Y" ]; then
        echo -n "Enter your custom filename: "
        read -r custom_filename
        if [ -n "$custom_filename" ]; then
            local clean_title="$(sanitize_filename "$custom_filename" | tr -d ' ')"
        else
            echo "Invalid filename. Proceeding with original filename."
        fi
    else
        echo "Proceeding with original filename."
    fi
    
    if [ ! -w "$KINDLE_DOCUMENTS" ]; then
        echo "No write permission in $KINDLE_DOCUMENTS" >&2
        return 1
    fi

    if [ "$CREATE_SUBFOLDERS" = "true" ]; then
        local book_folder="$KINDLE_DOCUMENTS/$clean_title"
        if ! mkdir -p "$book_folder"; then
            echo "Failed to create folder '$book_folder'" >&2
            return 1
        fi
        local final_location="$book_folder/$clean_title.$format"
    else
        local final_location="$KINDLE_DOCUMENTS/$clean_title.$format"
    fi

    if [ -e "$final_location" ] && [ ! -w "$final_location" ]; then
        echo "No permission to overwrite $final_location" >&2
        return 1
    fi

    printf '\nFetching download page...\n'

    # `if ! local x="$(...)"` tested the exit status of `local`, not of the
    # command substitution, so neither of the old checks here could ever fire.
    #
    # The mirror that served the search will not necessarily serve this page -
    # Libgen hosts answer inconsistently - so walk the candidates instead of
    # giving up on the first miss.
    local page="$TMP_DIR/.kf_search.$$dl"
    local mirror served=""
    for mirror in $LGLI_URL $LGLI_MIRROR_URLS; do
        [ -n "$mirror" ] || continue
        if http_fetch "$page" "$mirror/ads.php?md5=$md5" --max-time 20 \
           && grep -q 'get\.php' "$page" 2>/dev/null; then
            served="$mirror"
            break
        fi
    done

    if [ -z "$served" ]; then
        rm -f "$page"
        echo "Could not fetch a download page for this book (${NET_FAIL_STAGE:-no link found})." >&2
        return 1
    fi

    local download_link
    download_link="$(grep -o 'href="[^"]*get\.php[^"]*"' "$page" 2>/dev/null | head -1 | cut -d'"' -f2)"
    rm -f "$page"

    if [ -z "$download_link" ]; then
        echo "No download link found" >&2
        return 1
    fi

    # Mirrors emit this link absolute, root-relative or bare; the old code
    # always glued it on with a slash, producing a double slash for the second.
    local download_url
    case "$download_link" in
        http*) download_url="$download_link" ;;
        /*)    download_url="$served$download_link" ;;
        *)     download_url="$served/$download_link" ;;
    esac
    echo "Downloading from: $download_url"

    printf '\nProgress (Press Ctrl + c to stop):\n'

    if curl -# -L --max-time 900 -A "$USER_AGENT" -o "$final_location" "$download_url"; then
        printf '\nDownload successful!\n'
        echo "Saved to: $final_location"
        return 0
    
    else
        printf '\nDownload failed.' >&2
        return 1
    fi
}