#!/bin/sh

zlib_download() {
    local index="$1"
    
    if [ ! -f "$TMP_DIR/search_results.json" ]; then
        echo "No search results found" >&2
        return 1
    fi
    
    local book_info="$(awk -v i="$index" 'BEGIN{RS="\\{"; FS="\\}"} NR==i+1{print $1}' "$TMP_DIR"/search_results.json)"
    if [ -z "$book_info" ]; then
        echo "Invalid book selection" >&2
        return 1
    fi
    
    local md5="$(get_json_value "$book_info" "md5")"
    local book_id="$(get_json_value "$book_info" "id")"
    local book_hash="$(get_json_value "$book_info" "hash")"

    # Z-Library search results already carry the id and hash. Only records from
    # the other sources need this lookup, which goes through the website and so
    # is behind DiamWall.
    if [ -z "$book_id" ] || [ -z "$book_hash" ]; then
        local final_url="$(curl -s -L --max-time 20 -A "$USER_AGENT" \
            -o /dev/null -w "%{url_effective}" "$ZLIB_URL/md5/$md5")"

        book_id="$(echo "$final_url" | sed -n 's#.*/book/\([0-9][0-9]*\)/[a-z0-9]\+#\1#p')"
        book_hash="$(echo "$final_url" | sed -n 's#.*/book/[0-9][0-9]*/\([a-z0-9]\+\).*#\1#p')"

        if [ -z "$book_id" ] || [ -z "$book_hash" ]; then
            echo "Failed to extract book info from URL: $final_url" >&2
            return 1
        fi
    fi

    # The login cookies are scoped to the mirror the login went through, which
    # need not be the one search settled on. The API accepts them on every
    # mirror, so send them explicitly instead of relying on domain matching.
    local auth_cookie="$(zlib_auth_cookie)"
    local cookie_opt="-b" cookie_val="$ZLIB_COOKIES_FILE"
    if [ -n "$auth_cookie" ]; then
        cookie_opt="-H"
        cookie_val="Cookie: $auth_cookie"
    fi

    local respfile="$TMP_DIR/.kf_zlogin.$$dl"
    if ! http_fetch "$respfile" "$ZLIB_URL/eapi/book/$book_id/$book_hash/file" \
            --max-time 25 --max-redirs 3 "$cookie_opt" "$cookie_val" \
            -H "Accept: application/json"; then
        # The API reports an expired login or a spent daily limit as a JSON
        # error with a 4xx status. That is not a block, so show its message.
        local api_error="$(get_json_value "$(cat "$respfile" 2>/dev/null)" "error")"
        rm -f "$respfile"
        if [ -n "$api_error" ]; then
            echo "Z-Library: $api_error" >&2
            case "$api_error" in
                *[Ll]ogin*) echo "Log in again from Settings > 2." >&2 ;;
            esac
        else
            zlib_report_block "$NET_FAIL_STAGE"
        fi
        return 1
    fi

    # A challenge page here is why upstream reported "Failed to get download
    # link" - the request never reached the API at all.
    if is_junk_response "$respfile" || ! grep -Fq '{' "$respfile" 2>/dev/null; then
        zlib_report_block "challenge"
        rm -f "$respfile"
        return 1
    fi

    local response="$(cat "$respfile")"
    rm -f "$respfile"

    local ddl="$(get_json_value "$response" "downloadLink" | sed 's#\\\/#/#g' | tr -d '\r\n')"
    local title="$(get_json_value "$response" "description" | tr -d '\r\n')"
    local ext="$(get_json_value "$response" "extension" | tr -d '\r\n')"

    if [ -z "$ddl" ]; then
        echo "Failed to get download link from Z-Library response." >&2
        echo "$response" | head -n1
        return 1
    fi

    printf '\nDo you want to change filename? [y/N]: '
    read -r confirm
    if [ "$confirm" = "y" ] || [ "$confirm" = "Y" ]; then
        printf '\nEnter your custom filename: '
        read -r custom_filename
        if [ -n "$custom_filename" ]; then
            local title="$(sanitize_filename "$custom_filename" | tr -d ' ')"
        else
            echo "Invalid filename. Proceeding with original filename."
        fi
    else
        echo "Proceeding with original filename."
    fi

    local file_size="$(curl -sI --max-time 20 -A "$USER_AGENT" "$ddl" \
        | awk '/Content-Length/ {printf "%.2f MB\n", $2/1048576}')"
    local filename="$(sanitize_filename "${title}.${ext}")"
    local filename="${filename:-book.bin}"
    
    if [ ! -w "$KINDLE_DOCUMENTS" ]; then
        echo "No write permission in $KINDLE_DOCUMENTS" >&2
        return 1
    fi

    if [ "$CREATE_SUBFOLDERS" = "true" ]; then
        local book_folder="$KINDLE_DOCUMENTS/$filename"
        if ! mkdir -p "$book_folder"; then
            echo "Failed to create folder '$book_folder'" >&2
            return 1
        fi
        local final_location="$book_folder/$filename"
    else
        local final_location="$KINDLE_DOCUMENTS/$filename"
    fi

    if [ -e "$final_location" ] && [ ! -w "$final_location" ]; then
        echo "No permission to overwrite $final_location" >&2
        return 1
    fi

    printf '\nDownloading:\n'
    printf "\nBook: $title\nExtension: $ext\nFile size: $file_size\nMD5: $md5\n"
    printf "\nProgress (Press Ctrl + c to stop):\n"

    if curl -L --progress-bar --max-time 600 -A "$USER_AGENT" \
            -b "$ZLIB_COOKIES_FILE" -o "$final_location" "$ddl"; then
        printf "\nDownload successful!\n"
        echo "Saved to: $final_location"
        return 0
    else
        echo "Download failed." >&2
        return 1
    fi
}