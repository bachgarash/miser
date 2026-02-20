APP     := miser
PKG     := miser/cmd
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w \
	-X $(PKG).Version=$(VERSION) \
	-X $(PKG).Commit=$(COMMIT) \
	-X $(PKG).Date=$(DATE)

.PHONY: build install run lint test clean

build:
	go build -ldflags '$(LDFLAGS)' -o $(APP) .

install:
	go install -ldflags '$(LDFLAGS)' .

run: build
	./$(APP)

lint:
	go vet ./...

test:
	go test ./...

clean:
	rm -f $(APP)
