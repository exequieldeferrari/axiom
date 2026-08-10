BINARY := bin/axiom
PKG := ./cmd/axiom

.PHONY: build test lint run clean

build:
	@mkdir -p bin
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed on:" && gofmt -l . && exit 1)
	go vet ./...

run:
	go run $(PKG)

clean:
	rm -rf bin
