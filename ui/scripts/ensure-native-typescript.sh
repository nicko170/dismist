#!/bin/sh
# npm sometimes skips TypeScript's optional per-platform binary
# (@typescript/typescript-<os>-<arch>) during `npm ci`, and every tsc run then
# fails with "Unable to resolve @typescript/typescript-…". When it is missing,
# fetch the matching version with `npm pack` and unpack it into node_modules,
# leaving package.json, the lockfile and npm's tree untouched.
set -eu
missing=$(node -e '
const v = require("typescript/package.json").version;
const p = `@typescript/typescript-${process.platform}-${process.arch}`;
try { require.resolve(`${p}/package.json`); } catch { process.stdout.write(`${p}@${v}`); }
')
[ -z "$missing" ] && exit 0
echo "installing missing $missing"
name=${missing%@*}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(cd "$tmp" && npm pack --silent "$missing" >/dev/null)
mkdir -p "node_modules/$name"
tar -xzf "$tmp"/*.tgz -C "node_modules/$name" --strip-components=1
node -e "require.resolve('$name/package.json')"
