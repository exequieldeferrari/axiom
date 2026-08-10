BINARY := bin/axiom
PKG := ./cmd/axiom

.PHONY: build test lint run clean

build:
	@mkdir -p bin
	go build -o $(BINARY) $(PKG)

test:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@abs="$$(cd . && pwd)/coverage.html"; \
	if command -v open >/dev/null 2>&1; then \
		open "$$abs" || true; \
	elif command -v xdg-open >/dev/null 2>&1; then \
		xdg-open "$$abs" >/dev/null 2>&1 || true; \
	elif command -v cmd.exe >/dev/null 2>&1; then \
		cmd.exe /c start "" "$$abs" || true; \
	else \
		echo "Coverage report: $$abs"; \
	fi

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:" && gofmt -l . && exit 1)
	go vet ./...

run:
	go run $(PKG)

clean:
	rm -rf bin coverage.out coverage.html
