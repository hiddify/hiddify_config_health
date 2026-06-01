BINARY  = hiddify-health
CMD     = ./cmd
VERSION = $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS = -ldflags "-s -w -X main.Version=$(VERSION)"

.PHONY: build test vet lint clean run-web cross

build:
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

test:
	go test ./...

test-v:
	go test -v ./...

vet:
	go vet ./...

lint: vet
	@which staticcheck > /dev/null 2>&1 && staticcheck ./... || echo "staticcheck not installed (go install honnef.co/go/tools/cmd/staticcheck@latest)"

clean:
	rm -f $(BINARY)

run-web: build
	./$(BINARY) serve --addr :8080

# Cross-compile for common targets
cross:
	GOOS=linux   GOARCH=amd64  go build $(LDFLAGS) -o dist/$(BINARY)-linux-amd64  $(CMD)
	GOOS=linux   GOARCH=arm64  go build $(LDFLAGS) -o dist/$(BINARY)-linux-arm64  $(CMD)
	GOOS=darwin  GOARCH=amd64  go build $(LDFLAGS) -o dist/$(BINARY)-darwin-amd64 $(CMD)
	GOOS=darwin  GOARCH=arm64  go build $(LDFLAGS) -o dist/$(BINARY)-darwin-arm64 $(CMD)
	GOOS=windows GOARCH=amd64  go build $(LDFLAGS) -o dist/$(BINARY)-windows-amd64.exe $(CMD)

# Run a quick local test of all examples (no real core binaries needed for check)
check-examples:
	@for dir in $$(find examples -name "run.json" -not -path "*/run.json" | xargs -I{} dirname {} | sort -u); do \
		echo "checking $$dir …"; \
		./$(BINARY) check "$$dir" || exit 1; \
	done
	@echo "all configs valid"
