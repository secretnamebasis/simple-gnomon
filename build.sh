#!/usr/bin/env bash
set -e

APP_NAME=simple-gnomon
VERSION=$(go run ./cmd/main.go -v)
DIST=build

mkdir -p $DIST

# Windows
GOOS=windows GOARCH=amd64 go build -o $DIST/$APP_NAME.exe ./cmd/main.go
(
  cd "$DIST"
  zip "$APP_NAME-windows-amd64-$VERSION.zip" "$APP_NAME.exe"
  rm "$APP_NAME.exe"
)

# Linux
GOOS=linux GOARCH=amd64 go build -o $DIST/$APP_NAME ./cmd/main.go
tar -czvf $DIST/$APP_NAME-linux-amd64-$VERSION.tar.gz -C $DIST $APP_NAME
rm $DIST/$APP_NAME

# Maybe?
GOOS=linux GOARCH=arm64 go build -o $DIST/$APP_NAME ./cmd/main.go
tar -czvf $DIST/$APP_NAME-linux-amd64-$VERSION.tar.gz -C $DIST $APP_NAME
rm $DIST/$APP_NAME

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -o $DIST/$APP_NAME ./cmd/main.go
tar -czvf $DIST/$APP_NAME-darwin-amd64-$VERSION.tar.gz -C $DIST $APP_NAME
rm $DIST/$APP_NAME

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o $DIST/$APP_NAME ./cmd/main.go
tar -czvf $DIST/$APP_NAME-darwin-arm64-$VERSION.tar.gz -C $DIST $APP_NAME
rm $DIST/$APP_NAME
