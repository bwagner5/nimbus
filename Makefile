.PHONY: build clean

build:
	GOOS=linux GOARCH=amd64 go build -o dist/nimbus ./cmd/nimbus
	GOOS=linux GOARCH=arm64 go build -o dist/nimbus-arm64 ./cmd/nimbus
	GOOS=darwin GOARCH=arm64 go build -o dist/nimbus-mac-arm64 ./cmd/nimbus

clean:
	rm -rf dist
