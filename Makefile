.PHONY: build clean

build:
	GOOS=linux GOARCH=amd64 go build -o dist/nimbus ./cmd/nimbus

clean:
	rm -rf dist
