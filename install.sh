#!/bin/sh
# Install the newest stable Hive or Bee release. No language runtime is required.
set -eu

fail() { printf 'Install failed: %s\n' "$*" >&2; exit 1; }
case "${1:-}" in
    hive|bee) component=$1; shift ;;
    *) fail 'Usage: sh install.sh hive|bee [--install-dir /absolute/path]' ;;
esac
destination="$HOME/.local/share/herdr-$component"
if [ "$#" -gt 0 ]; then
    [ "$#" -eq 2 ] && [ "$1" = --install-dir ] || fail 'Expected --install-dir /absolute/path'
    destination=$2
fi
case "$destination" in /*) ;; *) fail 'Installation directory must be absolute' ;; esac
[ ! -e "$destination" ] && [ ! -L "$destination" ] ||
    fail "Installation path already exists: $destination. Use the component's update command."

for command in curl tar awk sort mktemp; do
    command -v "$command" >/dev/null 2>&1 || fail "Required command not found: $command"
done
if command -v sha256sum >/dev/null 2>&1; then
    checksum=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    checksum=shasum
else
    fail 'Install sha256sum or shasum first'
fi
if [ "$component" = bee ]; then
    command -v herdr >/dev/null 2>&1 || fail 'Install Herdr first, then run the Bee installer again'
fi
case "$(uname -s)" in Darwin) system=darwin ;; Linux) system=linux ;; *) fail 'Supported systems: macOS and Linux' ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail 'Supported architectures: amd64 and arm64' ;; esac

temporary=$(mktemp -d "${TMPDIR:-/tmp}/herdr-install.XXXXXX")
incomplete=
cleanup() {
    rm -rf "$temporary"
    if [ -n "$incomplete" ]; then rm -rf "$incomplete"; fi
}
trap cleanup 0
trap 'exit 1' HUP INT TERM
download() {
    curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
        --connect-timeout 10 --max-time 60 --max-filesize "$3" \
        --output "$2" "$1"
}

# Read only fields directly inside each release object. String-aware scanning
# keeps nested assets and quoted release-note text out of version selection.
cat > "$temporary/releases.awk" <<'AWK'
function string_value(value) {
    if (depth == 2) {
        if (key == "tag_name") { tag = value; key = "" }
        else if (key != "") { key = "" }
        else { field = value }
    }
}
{
    for (i = 1; i <= length($0); i++) {
        c = substr($0, i, 1)
        if (quoted) {
            if (escaped) {
                # GitHub emits ASCII tags; unfamiliar escapes cannot match one.
                value = value ((c == "/" || c == "\\" || c == "\"") ? c : "\\" c)
                escaped = 0
            } else if (c == "\\") { escaped = 1 }
            else if (c == "\"") { quoted = 0; string_value(value) }
            else { value = value c }
            continue
        }
        if (c == "\"") { quoted = 1; value = ""; continue }
        if (c == "[" || c == "{") {
            depth++
            if (depth == 2 && c == "{") { tag = ""; draft = ""; pre = ""; key = ""; field = "" }
        } else if (c == "]" || c == "}") {
            if (depth == 2 && c == "}") {
                count++
                if (draft == "false" && pre == "false" && tag ~ ("^" component "/v[0-9]+\\.[0-9]+\\.[0-9]+$")) {
                    sub("^" component "/v", "", tag)
                    print tag
                }
            }
            depth--
        } else if (depth == 2 && c == ":") { key = field; field = "" }
        else if (depth == 2 && c == ",") { key = ""; field = "" }
        else if (depth == 2 && (c == "t" || c == "f")) {
            boolean = c == "t" ? "true" : "false"
            if (substr($0, i, length(boolean)) == boolean) {
                if (key == "draft") draft = boolean
                if (key == "prerelease") pre = boolean
                key = ""; i += length(boolean) - 1
            }
        }
    }
}
END {
    if (depth != 0 || quoted) exit 1
    print "count=" count + 0
}
AWK

printf 'Finding the newest stable %s release...\n' "$component"
page=1
: > "$temporary/versions"
while [ "$page" -le 10 ]; do
    download "https://api.github.com/repos/deepshape-ai/herdr-hive/releases?per_page=100&page=$page" \
        "$temporary/releases.json" 4194304
    awk -v component="$component" -f "$temporary/releases.awk" "$temporary/releases.json" > "$temporary/page"
    sed '/^count=/d' "$temporary/page" >> "$temporary/versions"
    count=$(sed -n 's/^count=//p' "$temporary/page")
    [ "$count" -eq 100 ] || break
    page=$((page + 1))
done
[ "$page" -le 10 ] || fail 'Release discovery exceeded 1,000 entries'
version=$(sort -t . -k1,1n -k2,2n -k3,3n "$temporary/versions" | tail -n 1)
[ -n "$version" ] || fail "No stable release found for $component"
asset="$component-$version-$system-$arch.tar.gz"
base="https://github.com/deepshape-ai/herdr-hive/releases/download/$component/v$version"
printf 'Downloading %s...\n' "$asset"
download "$base/SHA256SUMS" "$temporary/SHA256SUMS" 65536
download "$base/$asset" "$temporary/package.tar.gz" 67108864
expected=$(awk -v name="$asset" '$2 == name && NF == 2 { print $1 }' "$temporary/SHA256SUMS")
[ "${#expected}" -eq 64 ] || fail 'Missing or ambiguous release checksum'
case "$expected" in *[!0-9a-f]*) fail 'Invalid release checksum' ;; esac
if [ "$checksum" = sha256sum ]; then
    actual=$(sha256sum "$temporary/package.tar.gz" | awk '{print $1}')
else
    actual=$(shasum -a 256 "$temporary/package.tar.gz" | awk '{print $1}')
fi
[ "$actual" = "$expected" ] || fail 'Release checksum mismatch'

root="$component-$system-$arch"
tar -tzf "$temporary/package.tar.gz" > "$temporary/entries"
awk -v root="$root" -v component="$component" '
    NR > 16 { exit 1 }
    $0 == root "/" || $0 == root { next }
    { name = substr($0, length(root) + 2)
      if ($0 != root "/" name || seen[name]++ ||
          !(name == component || name == "README.md" || name == "README.zh.md" ||
            name == "LICENSE" || (component == "bee" && name == "herdr-plugin.toml"))) exit 1
    }
' "$temporary/entries" || fail 'Unexpected archive entry'
# Reject links and special files before extraction, on both BSD and GNU tar.
tar -tvzf "$temporary/package.tar.gz" > "$temporary/types"
awk 'substr($0,1,1) != "-" && substr($0,1,1) != "d" {exit 1}' "$temporary/types" || fail 'Unsupported archive entry type'
mkdir "$temporary/package"
(ulimit -f 131072; tar -xzf "$temporary/package.tar.gz" --no-same-owner \
    --strip-components=1 -C "$temporary/package")
for name in "$component" README.md LICENSE; do
    [ -f "$temporary/package/$name" ] || fail "Incomplete archive: missing $name"
done
chmod 644 "$temporary/package/"*
chmod 755 "$temporary/package/$component"
if [ "$component" = bee ]; then
    [ -f "$temporary/package/herdr-plugin.toml" ] || fail 'Missing plugin manifest'
    grep -Fx "version = \"$version\"" "$temporary/package/herdr-plugin.toml" >/dev/null || fail 'Plugin version does not match release'
fi

mkdir -p "$(dirname "$destination")"
# Exclusive creation prevents concurrent installers from overwriting each other.
mkdir "$destination" || fail "Cannot create new installation: $destination"
incomplete=$destination
cp "$temporary/package/"* "$destination/"
if [ "$component" = hive ]; then
    mkdir -m 700 "$destination/state"
fi
incomplete=
printf 'Installed %s %s in %s\n' "$component" "$version" "$destination"
if [ "$component" = bee ]; then
    herdr plugin link "$destination" --enabled </dev/null ||
        fail "Files installed. Retry: herdr plugin link '$destination' --enabled"
    printf 'Open Bee: herdr plugin pane open --plugin herdr.bee --entrypoint settings\n'
else
    printf 'Next: add device public keys to authorized_keys and start Hive as shown in the README.\n'
fi
