.PHONY: all build test builder clean

all: build

# The CLI binary. Builds with Go alone; the builder UI lives in the hoop.dev
# landing-page repo and is served at hoop.dev/labs/warden.
build:
	go build -o warden ./cmd/warden

test:
	go test ./...

# The web builder, in the default browser.
builder:
	open https://hoop.dev/labs/warden

clean:
	rm -rf warden
