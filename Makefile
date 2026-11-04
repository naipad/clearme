GO      ?= go
VERSION ?= 0.1.0
LDFLAGS ?= -s -w -X github.com/naipad/clearme/internal/version.Version=$(VERSION)
OUT     ?= dist

.PHONY: all build amd64 arm64 test vet checksums clean

all: build

build: amd64 arm64

amd64:
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/clearme-amd64 .

arm64:
	GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT)/clearme-arm64 .

checksums: build
	cd $(OUT) && sha256sum clearme-amd64 clearme-arm64 > sha256sums.txt

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -f $(OUT)/clearme-amd64 $(OUT)/clearme-arm64 $(OUT)/sha256sums.txt
