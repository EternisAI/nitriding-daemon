binary = nitriding
godeps = *.go go.mod go.sum
cover_out = cover.out
cover_html = cover.html

ARCH ?= amd64
OS ?= linux

all: lint test $(binary)

.PHONY: lint
lint: $(godeps)
	golangci-lint run
	go vet ./...
	govulncheck ./...

.PHONY: test
test: $(godeps)
	go test -cover ./...

.PHONY: coverage
coverage: $(cover_html)
	${BROWSER} $(cover_html)

$(cover_html): $(cover_out)
	go test -coverprofile=$(cover_out) .
	go tool cover -html=$(cover_out) -o $(cover_html)

$(binary): $(godeps)
	CGO_ENABLED=0 GOOS=$(OS) GOARCH=$(ARCH) go build -trimpath -ldflags="-s -w" -buildvcs=false -o $(binary)

.PHONY: clean
clean:
	rm -f $(binary)
	rm -f $(cover_out) $(cover_html)
