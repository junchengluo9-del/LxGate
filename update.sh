#!/bin/bash

# >>> lxgate identity (managed by tools/rebrand.sh; edit tools/rebrand.env) >>>
LXGATE_REPO="${LXGATE_REPO:-junchengluo9-del/LxGate}"
LXGATE_RAW_BASE="https://raw.githubusercontent.com/${LXGATE_REPO}"
LXGATE_RELEASE_BASE="https://github.com/${LXGATE_REPO}/releases"
# LXGATE_* variables are canonical; the legacy XUI_* names keep working as aliases.
# An LXGATE_ value wins over the XUI_ alias of the same name.
while IFS='=' read -r __lxgate_k __lxgate_v; do
    case "$__lxgate_k" in LXGATE_REPO | LXGATE_RAW_BASE | LXGATE_RELEASE_BASE) continue ;; esac
    export "XUI_${__lxgate_k#LXGATE_}=${__lxgate_v}"
done < <(env | grep -E '^LXGATE_[A-Z0-9_]+=' || true)
unset __lxgate_k __lxgate_v
# <<< lxgate identity <<<

red='\033[0;31m'
green='\033[0;32m'
blue='\033[0;34m'
yellow='\033[0;33m'
plain='\033[0m'

xui_folder="${XUI_MAIN_FOLDER:=/usr/local/lxgate}"
xui_service="${XUI_SERVICE:=/etc/systemd/system}"

# Don't edit this config
b_source="${BASH_SOURCE[0]}"
while [ -h "$b_source" ]; do
    b_dir="$(cd -P "$(dirname "$b_source")" > /dev/null 2>&1 && pwd || pwd -P)"
    b_source="$(readlink "$b_source")"
    [[ $b_source != /* ]] && b_source="$b_dir/$b_source"
done
cur_dir="$(cd -P "$(dirname "$b_source")" > /dev/null 2>&1 && pwd || pwd -P)"
script_name=$(basename "$0")

# Check command exist function
_command_exists() {
    type "$1" &> /dev/null
}

# Fail, log and exit script function
_fail() {
    local msg=${1}
    echo -e "${red}${msg}${plain}"
    exit 2
}

# Records this run's outcome for the panel's web updater to poll, since it
# launches this script detached and has no other way to learn whether it
# finished. Written to a fixed path outside XUI_MAIN_FOLDER so it survives
# the update regardless of what happens to that folder. The EXIT trap below
# covers every exit path in this file, including the bare `exit 1`/`exit 2`
# calls that don't go through _fail.
xui_update_run_id="${XUI_UPDATE_RUN_ID:-0}"
[[ "${xui_update_run_id}" =~ ^[0-9]+$ ]] || xui_update_run_id="0"
xui_update_status_file="${XUI_UPDATE_STATUS_FILE:-/etc/lxgate/update-status.json}"

_write_update_status() {
    local state="$1"
    local exit_code="$2"
    local status_dir
    status_dir="$(dirname "${xui_update_status_file}")"
    mkdir -p "${status_dir}" > /dev/null 2>&1
    local tmp_file="${xui_update_status_file}.tmp.$$"
    printf '{"runId":"%s","state":"%s","exitCode":%s,"finishedAt":%s}\n' \
        "${xui_update_run_id}" "${state}" "${exit_code}" "$(date +%s)" > "${tmp_file}" 2> /dev/null
    mv -f "${tmp_file}" "${xui_update_status_file}" > /dev/null 2>&1
}

_report_update_exit() {
    local code=$?
    if [[ "${code}" -eq 0 ]]; then
        _write_update_status "success" "0"
    else
        _write_update_status "failed" "${code}"
    fi
}
trap _report_update_exit EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

# check root
[[ $EUID -ne 0 ]] && _fail "FATAL ERROR: Please run this script with root privilege."

if _command_exists curl; then
    curl_bin=$(which curl)
else
    _fail "ERROR: Command 'curl' not found."
fi
# The panel sets XUI_UPDATE_PROXY to its egress bridge's socks5h URL for the GitHub downloads.
# The IP lookups use plain curl so they still report this server's own address.
[[ -n "${XUI_UPDATE_PROXY}" ]] && curl_bin="${curl_bin} --proxy ${XUI_UPDATE_PROXY}"

# >>> private release repository support >>>
# LxGate's release repository is private. The panel stores a read-only token in
# its update settings and hands it over both in the environment and as a
# 0600 file beside the database, because the panel's detached update process
# does not always inherit the panel's environment.
github_token="${XUI_GITHUB_TOKEN:-}"
if [[ -z "${github_token}" ]]; then
    github_token_file="${XUI_GITHUB_TOKEN_FILE:-/etc/lxgate/github-token}"
    if [[ -r "${github_token_file}" ]]; then
        github_token="$(tr -d '\r\n' < "${github_token_file}")"
    fi
fi
# gh_auth carries the headers every GitHub API call needs when a token exists.
gh_auth=()
if [[ -n "${github_token}" ]]; then
    gh_auth=(-H "Authorization: Bearer ${github_token}" -H "Accept: application/vnd.github+json" -H "X-GitHub-Api-Version: 2022-11-28")
fi

# _json_asset_id reads a release JSON payload on stdin and prints the id of the
# asset whose name is $1. The payload is split on '{' so each object gets its own
# line and the name/id pair can be matched without a JSON parser; update.sh runs
# on hosts where jq is not installed.
_json_asset_id() {
    local name="$1"
    tr '{' '\n' \
        | grep -F -e "\"name\":\"${name}\"" -e "\"name\": \"${name}\"" \
        | grep -o -E '"id"[[:space:]]*:[[:space:]]*[0-9]+' \
        | head -n 1 \
        | grep -o -E '[0-9]+'
}

# _download_release_asset <tag> <asset-name> <dest>
# With a token the asset is resolved through the API and fetched as
# application/octet-stream, which is the documented way to download from a
# private repository; the public web download URL answers 404 there. Without a
# token (a public fork, or an older install) the web URL is used as before.
_download_release_asset() {
    local tag="$1" name="$2" dest="$3" asset_id rc
    rm -f "${dest}"
    if [[ -n "${github_token}" ]]; then
        asset_id=$(${curl_bin} -Ls "${gh_auth[@]}" "https://api.github.com/repos/${LXGATE_REPO}/releases/tags/${tag}" 2> /dev/null | _json_asset_id "${name}")
        if [[ -n "${asset_id}" ]]; then
            ${curl_bin} -fLRo "${dest}" -H "Authorization: Bearer ${github_token}" -H "Accept: application/octet-stream" "https://api.github.com/repos/${LXGATE_REPO}/releases/assets/${asset_id}" 2> /dev/null
            rc=$?
            if [[ ${rc} -eq 0 && -s "${dest}" ]]; then
                return 0
            fi
            echo -e "${yellow}API asset download failed for ${name} (rc=${rc}), retrying via the web download URL${plain}"
            rm -f "${dest}"
        else
            echo -e "${yellow}${name} is not part of release ${tag}; continuing without it${plain}"
        fi
    fi
    ${curl_bin} -fLRo "${dest}" "${LXGATE_RELEASE_BASE}/download/${tag}/${name}" 2> /dev/null
}

# _fetch_repo_file <ref> <path> <dest> writes one repository file to dest.
# The raw host cannot serve a private repository, so a token switches the fetch
# to the contents API with the raw media type.
_fetch_repo_file() {
    local ref="$1" path="$2" dest="$3"
    if [[ -n "${github_token}" ]]; then
        ${curl_bin} -fsSL "${gh_auth[@]}" -H "Accept: application/vnd.github.raw" -o "${dest}" "https://api.github.com/repos/${LXGATE_REPO}/contents/${path}?ref=${ref}"
        return $?
    fi
    ${curl_bin} -fLRo "${dest}" "${LXGATE_RAW_BASE}/${ref}/${path}"
}

# _repo_file_status <ref> <path> prints the HTTP status of one repository file.
_repo_file_status() {
    local ref="$1" path="$2"
    if [[ -n "${github_token}" ]]; then
        ${curl_bin} -sIL "${gh_auth[@]}" -H "Accept: application/vnd.github.raw" -o /dev/null -w '%{http_code}' "https://api.github.com/repos/${LXGATE_REPO}/contents/${path}?ref=${ref}"
        return
    fi
    ${curl_bin} -sIL --retry 3 --connect-timeout 15 -o /dev/null -w '%{http_code}' "${LXGATE_RAW_BASE}/${ref}/${path}"
}
# <<< private release repository support <<<

# Check OS and set release variable
if [[ -f /etc/os-release ]]; then
    source /etc/os-release
    release=$ID
elif [[ -f /usr/lib/os-release ]]; then
    source /usr/lib/os-release
    release=$ID
else
    _fail "Failed to check the system OS, please contact the author!"
fi
echo "The OS release is: $release"

arch() {
    case "$(uname -m)" in
        x86_64 | x64 | amd64) echo 'amd64' ;;
        i*86 | x86) echo '386' ;;
        armv8* | armv8 | arm64 | aarch64) echo 'arm64' ;;
        armv7* | armv7 | arm) echo 'armv7' ;;
        armv6* | armv6) echo 'armv6' ;;
        armv5* | armv5) echo 'armv5' ;;
        s390x) echo 's390x' ;;
        *) echo -e "${red}Unsupported CPU architecture!${plain}" && rm -f "${cur_dir}/${script_name}" > /dev/null 2>&1 && exit 2 ;;
    esac
}

echo "Arch: $(arch)"

gen_random_string() {
    local length="$1"
    openssl rand -base64 $((length * 2)) \
        | tr -dc 'a-zA-Z0-9' \
        | head -c "$length"
}

xui_env_file_path() {
    case "${release}" in
        ubuntu | debian | armbian)
            echo "/etc/default/lxgate"
            ;;
        arch | manjaro | parch | alpine)
            echo "/etc/conf.d/lxgate"
            ;;
        *)
            echo "/etc/sysconfig/lxgate"
            ;;
    esac
}

load_xui_env() {
    local env_file
    env_file="$(xui_env_file_path)"
    if [[ -r "$env_file" ]]; then
        set -a
        # shellcheck disable=SC1090
        source "$env_file"
        set +a
    fi
}

install_base() {
    echo -e "${green}Updating and install dependency packages...${plain}"
    case "${release}" in
        ubuntu | debian | armbian)
            apt-get update > /dev/null 2>&1 && apt-get install -y -q cron curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        fedora | amzn | virtuozzo | rhel | almalinux | rocky | ol)
            dnf makecache -y > /dev/null 2>&1 && dnf install -y -q cronie curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        centos)
            if [[ "${VERSION_ID}" =~ ^7 ]]; then
                yum makecache -y > /dev/null 2>&1 && yum install -y -q cronie curl tar tzdata socat openssl > /dev/null 2>&1
            else
                dnf makecache -y > /dev/null 2>&1 && dnf install -y -q cronie curl tar tzdata socat openssl > /dev/null 2>&1
            fi
            ;;
        arch | manjaro | parch)
            pacman -Sy --noconfirm cronie curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        opensuse-tumbleweed | opensuse-leap)
            zypper refresh > /dev/null 2>&1 && zypper -q install -y cron curl tar timezone socat openssl > /dev/null 2>&1
            ;;
        alpine)
            apk update > /dev/null 2>&1 && apk add dcron curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        *)
            apt-get update > /dev/null 2>&1 && apt install -y -q cron curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
    esac
}

config_after_update() {
    local panel_needs_restart=0

    echo -e "${yellow}lxgate settings:${plain}"
    ${xui_folder}/lxgate setting -show true
    ${xui_folder}/lxgate migrate

    # Properly detect empty cert by checking if cert: line exists and has content after it
    local existing_cert=$(${xui_folder}/lxgate setting -getCert true 2> /dev/null | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
    local settings existing_webBasePath
    if ! settings=$("${xui_folder}/lxgate" setting -show true) ||
        ! existing_webBasePath=$(printf '%s\n' "$settings" | awk '
            /^get .* failed, error info:/ { failed = 1 }
            /^hasDefaultCredential: (true|false)$/ { credentials++ }
            /^port: [0-9]+$/ { ports++ }
            /^webBasePath:/ { paths++; sub(/^webBasePath: ?/, ""); path = $0 }
            END {
                if (failed || credentials != 1 || ports != 1 || paths != 1 || (path != "" && path !~ /^\//)) exit 1
                print path
            }'); then
        echo -e "${red}Unable to read webBasePath; leaving existing settings unchanged.${plain}" >&2
        return 1
    fi
    existing_webBasePath=${existing_webBasePath#/}
    local existing_port=$(printf '%s\n' "$settings" | awk '/^port: / {print $2}')

    # Get server IP
    local URL_lists=(
        "https://api4.ipify.org"
        "https://ipv4.icanhazip.com"
        "https://v4.api.ipinfo.io/ip"
        "https://ipv4.myexternalip.com/raw"
        "https://4.ident.me"
        "https://check-host.net/ip"
    )
    local server_ip=""
    for ip_address in "${URL_lists[@]}"; do
        local response=$(curl -s -w "\n%{http_code}" --max-time 3 "${ip_address}" 2> /dev/null)
        local http_code=$(echo "$response" | tail -n1)
        local ip_result=$(echo "$response" | head -n-1 | tr -d '[:space:]"')
        if [[ "${http_code}" == "200" && "${ip_result}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            server_ip="${ip_result}"
            break
        fi
    done

    # Only used to print the access URL; never prompt, the web updater has no TTY.
    if [[ -z "$server_ip" ]]; then
        server_ip=$(hostname -I 2> /dev/null | awk '{print $1}')
        server_ip="${server_ip:-<server-ip>}"
    fi

    # Only an unset or default root path needs initialization.
    if [[ -z "$existing_webBasePath" ]]; then
        echo -e "${yellow}WebBasePath is missing or set to root. Generating a new one...${plain}"
        local config_webBasePath=$(gen_random_string 18)
        ${xui_folder}/lxgate setting -webBasePath "${config_webBasePath}"
        existing_webBasePath="${config_webBasePath}"
        panel_needs_restart=1
        echo -e "${green}New WebBasePath: ${config_webBasePath}${plain}"
    fi

    # An update only updates the panel: TLS stays exactly as configured. Set it up
    # explicitly from the lxgate menu (SSL Certificate Management).
    local access_scheme="http" access_host="${server_ip}"
    if [[ -n "$existing_cert" ]]; then
        access_scheme="https"
        access_host=$(basename "$(dirname "$existing_cert")")
    fi
    echo ""
    echo -e "${green}═══════════════════════════════════════════${plain}"
    echo -e "${green}     Panel Access Information              ${plain}"
    echo -e "${green}═══════════════════════════════════════════${plain}"
    echo -e "${green}Access URL: ${access_scheme}://${access_host}:${existing_port}/${existing_webBasePath}${plain}"
    echo -e "${green}═══════════════════════════════════════════${plain}"
    if [[ -z "$existing_cert" ]]; then
        echo -e "${yellow}No SSL certificate is configured; the panel is served over HTTP.${plain}"
        echo -e "${yellow}Run 'lxgate' and choose SSL Certificate Management to set one up.${plain}"
    fi

    if [[ "$panel_needs_restart" -eq 1 ]]; then
        echo -e "${yellow}Restarting panel to apply the new web base path...${plain}"
        systemctl restart lxgate 2> /dev/null || rc-service lxgate restart 2> /dev/null
    fi
}

# setup_fail2ban auto-installs and configures fail2ban for the IP Limit feature
# by invoking the freshly downloaded lxgate CLI. IP Limit is load-bearing on
# fail2ban (without it the panel disables the limitIp field and zeroes existing
# limits), so updating an older install should make it work without a manual
# trip through the IP Limit menu. Non-fatal: a fail2ban failure must never abort
# the update. XUI_ENABLE_FAIL2BAN is honored (load_xui_env exports it from the
# persisted env file, so a deliberate opt-out survives updates).
setup_fail2ban() {
    if [[ -n "${XUI_ENABLE_FAIL2BAN+x}" && "${XUI_ENABLE_FAIL2BAN}" != "true" ]]; then
        echo -e "${yellow}XUI_ENABLE_FAIL2BAN=${XUI_ENABLE_FAIL2BAN}, skipping Fail2ban auto-setup.${plain}"
        return 0
    fi

    if [[ ! -x /usr/bin/lxgate ]]; then
        echo -e "${yellow}lxgate CLI not found; skipping Fail2ban auto-setup.${plain}"
        return 0
    fi

    # Scripts older than v3.4.0 have no setup-fail2ban and exit 0 from the
    # usage banner, which would read as success here.
    if ! grep -q '"setup-fail2ban")' /usr/bin/lxgate; then
        echo -e "${yellow}This lxgate.sh predates 'lxgate setup-fail2ban'; skipping Fail2ban auto-setup.${plain}"
        return 0
    fi

    echo -e "${green}Setting up Fail2ban for the IP Limit feature...${plain}"
    if /usr/bin/lxgate setup-fail2ban; then
        echo -e "${green}Fail2ban setup complete.${plain}"
    else
        echo -e "${yellow}Fail2ban setup did not finish; IP Limit stays disabled until you run 'lxgate' and open the IP Limit menu. Continuing.${plain}"
    fi
    return 0
}

# The hardened unit makes /usr, /boot, /efi and /etc read-only. The panel's own
# updater is expected to escape that sandbox by running this script through a
# transient systemd-run unit; when systemd-run is unavailable it starts this
# script as a plain child instead, and that child inherits the sandbox and then
# cannot write anything this update needs. Say so once, up front, instead of
# dying partway through with "Failed to download lxgate".
require_writable_update_paths() {
    local dir probe
    for dir in "${xui_folder%/*}" "/usr/bin"; do
        [[ -n "$dir" && -d "$dir" ]] || continue
        probe="${dir}/.lxgate-write-test.$$"
        # A real write test rather than [[ -w ]]: this runs as root, where a
        # permission bit means little and the test only reflects the file mode
        # and the mount flags, not an immutable attribute or a full filesystem.
        if ! : > "$probe" 2> /dev/null; then
            _fail "ERROR: ${dir} is not writable for this process (read-only mount, attribute or full filesystem). The panel's fallback updater cannot run inside the hardened systemd sandbox; update from the panel UI (which uses systemd-run) or run 'lxgate update' in a shell."
        fi
        rm -f "$probe"
    done
}

# Major version of the local systemd, 0 when it cannot be determined. The
# SystemCallFilter=@system-service group only exists from systemd 239 on (other
# @-named groups exist since 231); on older versions an unknown group is not
# ignored safely, the filter stays in force and leaves a whitelist the panel
# cannot run under.
_xui_systemd_major_version() {
    local version=""
    if command -v systemctl > /dev/null 2>&1; then
        version="$(systemctl --version 2>/dev/null | awk 'NR == 1 {print $2}')"
    fi
    if [[ ! "$version" =~ ^[0-9]+$ ]]; then
        echo 0
        return 0
    fi
    echo "$version"
}

# The shipped units list hardening that older systemd does not know: the
# directive is logged and ignored at load time rather than rejected, so the
# panel still starts, only without that protection. Each entry is the systemd
# release that introduced the directive (systemd.exec(5)); everything else in
# the unit predates the oldest systemd install.sh supports (CentOS 7 has 219).
# SystemCallFilter= is listed because the drop-in only writes it from 239 on.
_xui_warn_unsupported_hardening() {
    local version entry missing=""
    version="$(_xui_systemd_major_version)"
    [[ "$version" -gt 0 ]] || return 0
    for entry in RestrictRealtime:231 ReadWritePaths:231 ProtectKernelTunables:232 \
        ProtectKernelModules:232 RestrictNamespaces:233 LockPersonality:235 \
        SystemCallFilter:239 ProtectHostname:242 RestrictSUIDSGID:242 \
        ProtectKernelLogs:244 ProtectClock:245; do
        if [[ "$version" -lt "${entry##*:}" ]]; then
            missing="${missing:+$missing, }${entry%%:*} (${entry##*:})"
        fi
    done
    [[ -n "$missing" ]] || return 0
    echo -e "${yellow}Note: systemd ${version} ignores part of the hardening in lxgate.service; the panel still starts.${plain}"
    echo "      Not applied, needs a newer systemd: ${missing}."
    if [[ "$version" -lt 231 ]]; then
        echo "      The panel's folders stay writable through ReadWriteDirectories=, the alias this script installs."
    fi
    echo "      The rest of the hardening is in force. Upgrade systemd to apply the above."
    return 0
}

# ProtectSystem=full makes /usr, /boot, /efi and /etc read-only. ProtectSystem=
# strict would make the whole hierarchy read-only (only the kernel API
# filesystems stay as they are), and that would break the panel's own use of
# /tmp. The panel's stores are configurable (XUI_DB_FOLDER, XUI_LOG_FOLDER,
# XUI_BIN_FOLDER), and XUI_MAIN_FOLDER is the folder install.sh/update.sh place
# the files in -- the unit's WorkingDirectory on a stock install, and what a
# relative XUI_BIN_FOLDER is resolved against. So a hard-coded list in the unit
# either misses a relocated store -- the panel then cannot write its own SQLite
# database and sits in a Restart=on-failure loop -- or forces the operator to
# edit a file that every install/update overwrites from the release tarball.
# install.sh and update.sh therefore regenerate the drop-in from the folders
# actually in use, and the unit's own ReadWritePaths only carry the
# plain-install defaults. A relocated store means re-running install or update:
# the drop-in is only written here.
_xui_service_write_paths_dropin() {
    # $1 is the env file to resolve the XUI_* folders from; callers pass nothing
    # and get the OS-specific path the unit itself uses.
    local env_file="${1:-}"
    local dropin_dir dropin temp_file
    local db_folder log_folder bin_folder main_folder
    local path line="" whitespace_paths="" seen_paths="" escaped_path

    if [[ -z "$env_file" ]]; then
        env_file="$(xui_env_file_path)"
    fi
    if [[ -r "$env_file" ]]; then
        set -a
        # shellcheck disable=SC1090
        source "$env_file"
        set +a
    fi

    # XUI_* wins over the script's own default: the unit hands that same env
    # file to the panel through EnvironmentFile=, so these are the folders it
    # will actually use.
    main_folder="${XUI_MAIN_FOLDER:-${xui_folder}}"
    db_folder="${XUI_DB_FOLDER:-/etc/lxgate}"
    log_folder="${XUI_LOG_FOLDER:-/var/log/lxgate}"
    # An empty XUI_BIN_FOLDER resolves to "bin" relative to the panel's working
    # directory, which the unit sets to the main folder.
    bin_folder="${XUI_BIN_FOLDER:-bin}"
    if [[ "$bin_folder" != /* ]]; then
        bin_folder="${main_folder%/}/${bin_folder#./}"
    fi

    for path in "$db_folder" "$log_folder" "$bin_folder" "$main_folder"; do
        [[ "$path" == /* ]] || continue
        # ReadWritePaths= is a whitespace-separated list, and a folder whose
        # name contains whitespace cannot be written into it without relying on
        # quoting. A wrong entry makes systemd reject the whole drop-in and the
        # panel would not start, so leave such a folder out and say so instead.
        if [[ "$path" != "${path//[[:space:]]/}" ]]; then
            whitespace_paths="${whitespace_paths:+$whitespace_paths }$path"
            continue
        fi
        case " $seen_paths " in
            *" $path "*) continue ;;
        esac
        seen_paths="${seen_paths}${seen_paths:+ }$path"
        # systemd expands %-specifiers in unit files, so a folder name carrying
        # a literal % has to be written as %%, or the entry stops naming the
        # folder systemd is meant to keep writable.
        escaped_path="${path//%/%%}"
        line="${line} -${escaped_path}"
    done
    if [[ -n "$whitespace_paths" ]]; then
        echo "Warning: these folders contain whitespace and were left out of" >&2
        echo "         10-xui-sandbox.conf: $whitespace_paths" >&2
        echo "         The panel cannot write to them under the unit's sandbox." >&2
    fi
    line="${line# }"
    [[ -n "$line" ]] || return 1

    dropin_dir="${xui_service}/lxgate.service.d"
    dropin="${dropin_dir}/10-xui-sandbox.conf"
    temp_file="${dropin}.tmp.$$"

    mkdir -p "$dropin_dir" || return 1
    cat > "$temp_file" << EOF
# Regenerated by install.sh/update.sh on every install and update: edits here
# are lost, and the list only reflects the XUI_* variables read from
# ${env_file} at that moment. Re-run install/update after moving a store.
# It lists the folders the panel writes to. Put local additions in their own
# drop-in, for example 20-lxgate-local.conf, which nothing here touches.
[Service]
ReadWritePaths=${line}
ReadWriteDirectories=${line}
EOF
    if [[ "$(_xui_systemd_major_version)" -ge 239 ]]; then
        cat >> "$temp_file" << 'EOF'
# @system-service needs systemd >= 239; on older versions the unknown group
# would leave the panel with a filter it cannot start under (lxgate.service.*).
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM
EOF
    fi
    if [[ ! -s "$temp_file" ]]; then
        rm -f "$temp_file"
        return 1
    fi
    chmod 644 "$temp_file"
    mv -f "$temp_file" "$dropin" || { rm -f "$temp_file"; return 1; }
    if command -v systemctl > /dev/null 2>&1; then
        systemctl daemon-reload > /dev/null 2>&1 || true
    fi
    return 0
}

# Lands a systemd unit file at ${xui_service}/lxgate.service via a temp file +
# atomic mv, so a failed cp/curl or an interrupted mv never leaves a
# truncated unit file at the live path -- systemd would then fail to parse
# it on the next daemon-reload/start. Same pattern already used for
# /usr/bin/lxgate elsewhere in this script. source_is_url picks cp (from a
# file already extracted from the release tarball) vs curl (GitHub fallback).
_install_xui_service_unit() {
    local source="$1"
    local source_is_url="$2"
    local dest="${xui_service}/lxgate.service"
    local temp_file="${dest}.tmp.$$"

    rm -f "$temp_file"
    if [[ "$source_is_url" == "true" ]]; then
        ${curl_bin} -fLRo "$temp_file" "$source" > /dev/null 2>&1
    else
        cp -f "$source" "$temp_file" > /dev/null 2>&1
    fi
    if [[ $? -ne 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi
    if [[ ! -s "$temp_file" ]]; then
        rm -f "$temp_file"
        return 1
    fi
    mv -f "$temp_file" "$dest"
    if [[ $? -ne 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi
    if ! _xui_service_write_paths_dropin; then
        echo -e "${yellow}Warning: could not refresh ${xui_service}/lxgate.service.d/10-xui-sandbox.conf.${plain}"
        echo -e "${yellow}If XUI_DB_FOLDER or XUI_LOG_FOLDER points outside /etc/lxgate and /var/log/lxgate, the panel may not be able to write to it under ProtectSystem=full.${plain}"
    fi
    _xui_warn_unsupported_hardening
    return 0
}

# Older tags predate some of these files (lxgate.rc arrived in v2.8.4). Serving
# main's copy against an old binary is the mismatch this pinning exists to
# prevent, so probe before the old install is removed and refuse the tag.
require_repo_files() {
    local ref="$1" name status
    shift
    [[ "${ref}" == "main" ]] && return 0
    for name in "$@"; do
        status=$(_repo_file_status "${ref}" "${name}")
        if [[ "${status}" != "200" ]]; then
            _fail "ERROR: ${name} is not available for ${ref} (HTTP ${status}). Update to a release that ships it, or to 'dev-latest'. The current installation is untouched."
        fi
    done
}

update_lxgate() {
    cd ${xui_folder%/lxgate}/

    load_xui_env

    if [ -f "${xui_folder}/lxgate" ]; then
        current_xui_version=$(${xui_folder}/lxgate -v)
        echo -e "${green}Current lxgate version: ${current_xui_version}${plain}"
    else
        _fail "ERROR: Current lxgate version: unknown"
    fi

    echo -e "${green}Downloading new lxgate version...${plain}"

    # XUI_UPDATE_TAG lets the panel target a specific release tag (e.g. the
    # rolling dev-latest pre-release). Empty keeps the default latest-stable flow.
    if [[ -n "${XUI_UPDATE_TAG}" ]]; then
        tag_version="${XUI_UPDATE_TAG}"
        echo -e "${green}Using update tag: ${tag_version}${plain}"
    else
        tag_version=$(${curl_bin} -Ls "${gh_auth[@]}" "https://api.github.com/repos/${LXGATE_REPO}/releases/latest" 2> /dev/null | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
        if [[ ! -n "$tag_version" ]]; then
            # No stable release yet: releases/latest ignores pre-releases, so
            # fall back to the rolling dev build when it exists. With a token the
            # private repository is asked through the API; the web download URL
            # answers 404 for it.
            if [[ -n "${github_token}" ]]; then
                dev_code=$(${curl_bin} -sL "${gh_auth[@]}" -o /dev/null -w '%{http_code}' "https://api.github.com/repos/${LXGATE_REPO}/releases/tags/dev-latest" 2> /dev/null)
            else
                dev_code=$(${curl_bin} -sIL -o /dev/null -w '%{http_code}' "${LXGATE_RELEASE_BASE}/download/dev-latest/lxgate-linux-$(arch).tar.gz" 2> /dev/null)
            fi
            if [[ "${dev_code}" == "200" ]]; then
                echo -e "${yellow}No stable lxgate release found; updating to the rolling dev build (dev-latest).${plain}"
                tag_version="dev-latest"
            else
                _fail "ERROR: Failed to fetch lxgate version (no stable release and no dev-latest build, or GitHub API restrictions), please try it later"
            fi
        fi
    fi
    echo -e "Got lxgate latest version: ${tag_version}, beginning the installation..."
    # lxgate.rc and the unit files must come from the binary's own release; only the
    # rolling dev build tracks main. lxgate.sh ships in the archive and needs no GitHub check.
    script_ref="${tag_version}"
    if [[ "${tag_version}" == "dev-latest" ]]; then
        script_ref="main"
    fi
    # The unit files are only fetched when the release tarball lacks them, so
    # they are checked at that point instead of here.
    [[ $release == "alpine" ]] && require_repo_files "${script_ref}" "lxgate.rc"
    if ! _download_release_asset "${tag_version}" "lxgate-linux-$(arch).tar.gz" "${xui_folder}-linux-$(arch).tar.gz"; then
        _fail "ERROR: Failed to download lxgate, please be sure that your server can access GitHub"
    fi
    if [[ ! -s ${xui_folder}-linux-$(arch).tar.gz ]]; then
        rm ${xui_folder}-linux-$(arch).tar.gz -f > /dev/null 2>&1
        _fail "ERROR: Downloaded lxgate release archive is empty, please be sure that your server can access GitHub"
    fi
    # Releases publish <asset>.sha256 next to each archive. A mismatch or a
    # failed sidecar download aborts the update; only a 404 (releases
    # predating the sidecar) is tolerated with a warning.
    archive="${xui_folder}-linux-$(arch).tar.gz"
    rm -f "${archive}.sha256"
    if _download_release_asset "${tag_version}" "lxgate-linux-$(arch).tar.gz.sha256" "${archive}.sha256"; then
        sidecar_code="200"
    else
        rm -f "${archive}.sha256"
        sidecar_code="404"
    fi
    if [[ "${sidecar_code}" == "200" ]]; then
        expected_sha256=$(awk 'NR == 1 {print $1}' "${archive}.sha256")
        actual_sha256=$(sha256sum "${archive}" | awk '{print $1}')
        rm -f "${archive}.sha256"
        if [[ ! "${expected_sha256}" =~ ^[0-9a-f]{64}$ || "${expected_sha256}" != "${actual_sha256}" ]]; then
            rm -f "${archive}"
            _fail "ERROR: Checksum mismatch for $(basename "${archive}"): expected ${expected_sha256:-<none>}, got ${actual_sha256}"
        fi
        echo -e "${green}Checksum verified: ${actual_sha256}${plain}"
    elif [[ "${sidecar_code}" == "404" ]]; then
        rm -f "${archive}.sha256"
        echo -e "${yellow}No checksum published for this release, skipping verification${plain}"
    else
        rm -f "${archive}.sha256" "${archive}"
        _fail "ERROR: Failed to download the checksum for lxgate-linux-$(arch).tar.gz (HTTP ${sidecar_code})"
    fi

    # Download everything before lxgate stops, because its Xray and any panel egress bridge
    # stop with it. Alpine's init script is the only file the archive lacks.
    if [[ $release == "alpine" ]]; then
        echo -e "${green}Downloading startup unit lxgate.rc...${plain}"
        xui_rc_temp="/etc/init.d/lxgate.tmp.$$"
        rm -f "${xui_rc_temp}"
        _fetch_repo_file "${script_ref}" "lxgate.rc" "${xui_rc_temp}" > /dev/null 2>&1
        if [[ $? -ne 0 ]]; then
            rm -f "${xui_rc_temp}" "${archive}"
            _fail "ERROR: Failed to download startup unit lxgate.rc, please be sure that your server can access GitHub"
        fi
        if [[ ! -s "${xui_rc_temp}" ]]; then
            rm -f "${xui_rc_temp}" "${archive}"
            _fail "ERROR: Downloaded startup unit lxgate.rc is empty, please be sure that your server can access GitHub"
        fi
    fi

    if [[ -e ${xui_folder}/ ]]; then
        echo -e "${green}Stopping lxgate...${plain}"
        if [[ $release == "alpine" ]]; then
            if [ -f "/etc/init.d/lxgate" ]; then
                rc-service lxgate stop > /dev/null 2>&1
                rc-update del lxgate > /dev/null 2>&1
                echo -e "${green}Removing old service unit version...${plain}"
                rm -f /etc/init.d/lxgate > /dev/null 2>&1
            else
                rm lxgate-linux-$(arch).tar.gz -f > /dev/null 2>&1
                _fail "ERROR: lxgate service unit not installed."
            fi
        else
            if [ -f "${xui_service}/lxgate.service" ]; then
                systemctl stop lxgate > /dev/null 2>&1
                systemctl disable lxgate > /dev/null 2>&1
                echo -e "${green}Removing old systemd unit version...${plain}"
                rm ${xui_service}/lxgate.service -f > /dev/null 2>&1
                systemctl daemon-reload > /dev/null 2>&1
            else
                rm lxgate-linux-$(arch).tar.gz -f > /dev/null 2>&1
                _fail "ERROR: lxgate systemd unit not installed."
            fi
        fi
        # Kill any leftover mtg (MTProto) sidecars. lxgate runs them outside its own
        # lifecycle, so on Linux a stale one can survive the stop and keep holding
        # an inbound port with an outdated secret, silently breaking new clients.
        # The new panel respawns a clean mtg per inbound on next start.
        pkill -f 'mtg-linux-[^ ]* run ' > /dev/null 2>&1 || true
        pkill -f 'tuic-server.*-c .*bin/tuic/tuic_[0-9]+\.json' > /dev/null 2>&1 || true
        echo -e "${green}Removing old lxgate version...${plain}"
        rm ${xui_folder} -f > /dev/null 2>&1
        rm ${xui_folder}/lxgate.service -f > /dev/null 2>&1
        rm ${xui_folder}/lxgate.service.debian -f > /dev/null 2>&1
        rm ${xui_folder}/lxgate.service.arch -f > /dev/null 2>&1
        rm ${xui_folder}/lxgate.service.rhel -f > /dev/null 2>&1
        rm ${xui_folder}/lxgate -f > /dev/null 2>&1
        rm ${xui_folder}/lxgate.sh -f > /dev/null 2>&1
        echo -e "${green}Removing old mtg version...${plain}"
        rm ${xui_folder}/bin/mtg-linux-$(arch) -f > /dev/null 2>&1
        # By default the update keeps the Xray core the operator picked in the
        # panel (Xray Version) when it is a supported release; KEEP_XRAY=0
        # forces the bundled core, KEEP_XRAY=1 keeps any installed core.
        kept_xray=""
        supported_xray=" 26.10.21 26.9.30 26.7.28 26.7.11 26.6.27 "
        if [[ "${KEEP_XRAY:-auto}" != "0" ]]; then
            for xb in ${xui_folder}/bin/xray-linux-$(arch) ${xui_folder}/bin/xray-linux-arm32; do
                if [[ -s "$xb" ]]; then
                    xv="$("$xb" version 2>/dev/null | awk 'NR==1{print $2}')"
                    if [[ "${KEEP_XRAY:-auto}" == "1" || ( -n "$xv" && "$supported_xray" == *" $xv "* ) ]]; then
                        kept_xray="$(mktemp)" && cp -f "$xb" "$kept_xray" && kept_xray_name="$(basename "$xb")"
                        echo -e "${yellow}Keeping current Xray core ${xv:-?} ($kept_xray_name); set KEEP_XRAY=0 to use the bundled core${plain}"
                    else
                        echo -e "${yellow}Installed Xray core ${xv:-unknown} is not a supported release; installing the bundled core${plain}"
                    fi
                    break
                fi
            done
        fi
        echo -e "${green}Removing old xray version...${plain}"
        rm ${xui_folder}/bin/xray-linux-$(arch) -f > /dev/null 2>&1
        echo -e "${green}Removing old README and LICENSE file...${plain}"
        rm ${xui_folder}/bin/README.md -f > /dev/null 2>&1
        rm ${xui_folder}/bin/LICENSE -f > /dev/null 2>&1
        rm ${xui_folder}/bin/tuic-server -f > /dev/null 2>&1
        rm ${xui_folder}/bin/tuic -rf > /dev/null 2>&1
    else
        rm lxgate-linux-$(arch).tar.gz -f > /dev/null 2>&1
        _fail "ERROR: lxgate not installed."
    fi

    echo -e "${green}Installing new lxgate version...${plain}"
    tar zxvf lxgate-linux-$(arch).tar.gz > /dev/null 2>&1
    if [[ $? -ne 0 ]]; then
        rm lxgate-linux-$(arch).tar.gz -f > /dev/null 2>&1
        _fail "ERROR: Failed to extract the lxgate release archive -- the previous installation has already been removed, so the panel will not start until this is fixed; try running the update again"
    fi
    rm lxgate-linux-$(arch).tar.gz -f > /dev/null 2>&1
    cd lxgate > /dev/null 2>&1
    if [[ $? -ne 0 || ! -s lxgate ]]; then
        _fail "ERROR: Extracted lxgate archive is missing the lxgate binary -- the previous installation has already been removed, so the panel will not start until this is fixed; try running the update again"
    fi
    chmod +x lxgate > /dev/null 2>&1

    # Check the system's architecture and rename the file accordingly.
    # The panel binary maps GOARCH=arm to "arm32" (internal/xray/process.go),
    # so the Xray binary must be named xray-linux-arm32; mtg keeps plain "arm".
    if [[ $(arch) == "armv5" || $(arch) == "armv6" || $(arch) == "armv7" ]]; then
        mv bin/xray-linux-$(arch) bin/xray-linux-arm32 > /dev/null 2>&1
        chmod +x bin/xray-linux-arm32 > /dev/null 2>&1
        if [[ -f bin/mtg-linux-$(arch) ]]; then
            mv bin/mtg-linux-$(arch) bin/mtg-linux-arm > /dev/null 2>&1
            chmod +x bin/mtg-linux-arm > /dev/null 2>&1
        fi
    fi

    if [[ -n "${kept_xray:-}" && -s "$kept_xray" ]]; then
        cp -f "$kept_xray" "bin/${kept_xray_name}" && rm -f "$kept_xray"
        chmod +x "bin/${kept_xray_name}" > /dev/null 2>&1
    fi
    chmod +x lxgate bin/xray-linux-$(arch) > /dev/null 2>&1
    if [[ -f bin/mtg-linux-arm ]]; then
        chmod +x bin/mtg-linux-arm > /dev/null 2>&1
    elif [[ -f bin/mtg-linux-$(arch) ]]; then
        chmod +x bin/mtg-linux-$(arch) > /dev/null 2>&1
    fi

    # The archive's lxgate.sh is built from the same commit as the binary.
    echo -e "${green}Installing lxgate.sh script...${plain}"
    local xui_script_temp="/usr/bin/lxgate-temp.$$"
    if ! cp -f lxgate.sh "${xui_script_temp}" || ! mv -f "${xui_script_temp}" /usr/bin/lxgate; then
        rm -f "${xui_script_temp}"
        _fail "ERROR: Failed to install lxgate.sh script"
    fi

    chmod +x ${xui_folder}/lxgate.sh > /dev/null 2>&1
    chmod +x /usr/bin/lxgate > /dev/null 2>&1
    mkdir -p /var/log/lxgate > /dev/null 2>&1

    echo -e "${green}Changing owner...${plain}"
    chown -R root:root ${xui_folder} > /dev/null 2>&1

    if [ -f "${xui_folder}/bin/config.json" ]; then
        echo -e "${green}Changing on config file permissions...${plain}"
        chmod 640 ${xui_folder}/bin/config.json > /dev/null 2>&1
    fi

    # Finish the schema/data migrations before the service starts, so the service and
    # config_after_update's CLI calls never run them on the same database at once (#6728).
    echo -e "${green}Migrating database...${plain}"
    "${xui_folder}/lxgate" migrate

    if [[ $release == "alpine" ]]; then
        echo -e "${green}Installing startup unit lxgate.rc...${plain}"
        mv -f "${xui_rc_temp}" /etc/init.d/lxgate
        if [[ $? -ne 0 ]]; then
            rm -f "${xui_rc_temp}"
            _fail "ERROR: Failed to install startup unit lxgate.rc"
        fi
        chmod +x /etc/init.d/lxgate > /dev/null 2>&1
        chown root:root /etc/init.d/lxgate > /dev/null 2>&1
        rc-update add lxgate > /dev/null 2>&1
        rc-service lxgate start > /dev/null 2>&1
    else
        if [ -f "lxgate.service" ]; then
            echo -e "${green}Installing systemd unit...${plain}"
            if ! _install_xui_service_unit "lxgate.service" "false"; then
                echo -e "${red}Failed to copy lxgate.service${plain}"
                exit 1
            fi
        else
            service_installed=false
            case "${release}" in
                ubuntu | debian | armbian)
                    if [ -f "lxgate.service.debian" ]; then
                        echo -e "${green}Installing debian-like systemd unit...${plain}"
                        if _install_xui_service_unit "lxgate.service.debian" "false"; then
                            service_installed=true
                        fi
                    fi
                    ;;
                arch | manjaro | parch)
                    if [ -f "lxgate.service.arch" ]; then
                        echo -e "${green}Installing arch-like systemd unit...${plain}"
                        if _install_xui_service_unit "lxgate.service.arch" "false"; then
                            service_installed=true
                        fi
                    fi
                    ;;
                *)
                    if [ -f "lxgate.service.rhel" ]; then
                        echo -e "${green}Installing rhel-like systemd unit...${plain}"
                        if _install_xui_service_unit "lxgate.service.rhel" "false"; then
                            service_installed=true
                        fi
                    fi
                    ;;
            esac

            # If service file not found in tar.gz, download from GitHub
            if [ "$service_installed" = false ]; then
                echo -e "${yellow}Service files not found in tar.gz, downloading from GitHub...${plain}"
                case "${release}" in
                    ubuntu | debian | armbian)
                        service_unit_name="lxgate.service.debian"
                        ;;
                    arch | manjaro | parch)
                        service_unit_name="lxgate.service.arch"
                        ;;
                    *)
                        service_unit_name="lxgate.service.rhel"
                        ;;
                esac

                service_unit_tmp="$(mktemp)"
                if ! _fetch_repo_file "${script_ref}" "${service_unit_name}" "${service_unit_tmp}" \
                    || ! _install_xui_service_unit "${service_unit_tmp}" "false"; then
                    rm -f "${service_unit_tmp}"
                    echo -e "${red}Failed to install lxgate.service from GitHub (${script_ref}) -- the release tarball did not ship one either${plain}"
                    exit 1
                fi
                rm -f "${service_unit_tmp}"
            fi
        fi
        chown root:root ${xui_service}/lxgate.service > /dev/null 2>&1
        chmod 644 ${xui_service}/lxgate.service > /dev/null 2>&1
        systemctl daemon-reload > /dev/null 2>&1
        systemctl enable lxgate > /dev/null 2>&1
        systemctl start lxgate > /dev/null 2>&1
    fi

    config_after_update

    # IP Limit relies on fail2ban; install + configure it now so the feature
    # works out of the box on update too (no-op when XUI_ENABLE_FAIL2BAN=false).
    # Never fatal.
    setup_fail2ban

    echo -e "${green}lxgate ${tag_version}${plain} updating finished, it is running now..."
    echo -e ""
    echo -e "┌───────────────────────────────────────────────────────┐
│  ${blue}lxgate control menu usages (subcommands):${plain}              │
│                                                       │
│  ${blue}lxgate${plain}              - Admin Management Script          │
│  ${blue}lxgate start${plain}        - Start                            │
│  ${blue}lxgate stop${plain}         - Stop                             │
│  ${blue}lxgate restart${plain}      - Restart                          │
│  ${blue}lxgate status${plain}       - Current Status                   │
│  ${blue}lxgate settings${plain}     - Current Settings                 │
│  ${blue}lxgate enable${plain}       - Enable Autostart on OS Startup   │
│  ${blue}lxgate disable${plain}      - Disable Autostart on OS Startup  │
│  ${blue}lxgate log${plain}          - Check logs                       │
│  ${blue}lxgate banlog${plain}       - Check Fail2ban ban logs          │
│  ${blue}lxgate update${plain}       - Update                           │
│  ${blue}lxgate legacy${plain}       - Legacy version                   │
│  ${blue}lxgate install${plain}      - Install                          │
│  ${blue}lxgate uninstall${plain}    - Uninstall                        │
└───────────────────────────────────────────────────────┘"
}

echo -e "${green}Running...${plain}"
require_writable_update_paths
install_base
update_lxgate $1
