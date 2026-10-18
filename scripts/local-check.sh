#!/bin/sh
set -eu

go test ./...
go build -o /tmp/announcementd ./cmd/announcementd
