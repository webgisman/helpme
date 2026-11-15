#!/bin/sh
# Compile HelpMe.exe (Windows) et le relais (Linux), sans dépendance C. Résultat dans build/.
set -e
cd "$(dirname "$0")/.."
go test ./...
(cd cmd/helpme && go-winres make --in ../../winres/winres.json --arch amd64)
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o build/HelpMe.exe ./cmd/helpme
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o build/helpme-relay ./cmd/relay
(cd build && sha256sum HelpMe.exe > HelpMe.exe.sha256)
ls -l build
