#!/usr/bin/env -S bash -e

# Copyright 2019 The Go Authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

source devtools/lib.sh || {
  echo "Are you at repo root?"
  exit 1
}

usage() {
  cat <<EOUSAGE
Usage: $0 [up|down|force|version] {#}"
EOUSAGE
}

# Redirect stderr to stdout because migrate outputs to stderr, and we want
# to be able to use ordinary output redirection.
case "$1" in
up | down | force | version)
  # get migrate from https://github.com/golang-migrate/migrate/releases
  migrate \
    -source file:migrations \
    -database "postgres://outreach:$GO_DISCOVERY_DATABASE_PASSWORD@localhost:5432/pkgsite?sslmode=disable" \
    "$@" 2>&1
  ;;
*)
  usage
  exit 1
  ;;
esac
