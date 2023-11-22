#!/usr/bin/env bash

source x.sh
CURRENT_PATH=$(pwd)
PROJECT_PATH=$(dirname "${CURRENT_PATH}")
BUILD_PATH=${PROJECT_PATH}/bin
APP_VERSION=$(if [ "$(git rev-parse --abbrev-ref HEAD)" == "HEAD" ]; then git describe --tags HEAD; else echo "dev"; fi)

rm -rf "$BUILD_PATH"
mkdir -p "$BUILD_PATH"

print_blue "===> 1. Build axiom-ledger"
cd "${PROJECT_PATH}" && make package version="${APP_VERSION}"

print_blue "===> 2. Pack binary"
cd "${PROJECT_PATH}" || (echo "project path is not exist" && return)
if [ "$(uname)" == "Darwin" ]; then
  cd "${PROJECT_PATH}"
  mv ./axiom-ledger-"${APP_VERSION}".tar.gz ./axiom_ledger_darwin_x86_64_"${APP_VERSION}".tar.gz
else
  cd "${PROJECT_PATH}"
  mv ./axiom-ledger-"${APP_VERSION}".tar.gz ./axiom_ledger_linux_amd64_"${APP_VERSION}".tar.gz
fi