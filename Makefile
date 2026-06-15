GO ?= go
NPM ?= npm
GOPATH ?= $(CURDIR)/.gopath
DESKTOP_TAGS ?= desktop,production
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

export GOPATH

.PHONY: build desktop app frontend-install frontend-build frontend-check-dist package-macos package-linux release-checksums release-scan release-verify docker-image install clean distclean run test fmt smoke smoke-desktop runtime-start runtime-stop runtime-status runtime-bg desktop-run dev service-install service-uninstall doctor

build:
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/agx ./cmd/agx

desktop: frontend-install frontend-build
	$(GO) build -tags "$(DESKTOP_TAGS)" -o bin/agx-desktop ./desktop

app: build desktop

frontend-install:
	$(NPM) --prefix desktop/frontend ci

frontend-build:
	$(NPM) --prefix desktop/frontend run build

frontend-check-dist: frontend-install frontend-build
	test -f desktop/frontend/dist/index.html
	test -n "$$(find desktop/frontend/dist/assets -name 'index-*.js' -print -quit)"

package-macos:
	VERSION=$(VERSION) GOOS=darwin GOARCH=arm64 ./scripts/package-macos.sh

package-linux:
	VERSION=$(VERSION) GOOS=linux ./scripts/package-linux.sh

release-checksums:
	./scripts/release-checksums.sh

release-scan:
	./scripts/scan-release-artifacts.sh

release-verify:
	./scripts/release-verify.sh

docker-image:
	$(MAKE) -C docker build VERSION=$(VERSION)

install:
	$(GO) install ./cmd/agx

clean:
	rm -rf bin/

distclean: clean
	rm -rf .gopath/
	rm -rf desktop/frontend/node_modules/

run:
	$(GO) run ./cmd/agx

test: frontend-build
	$(GO) test ./...

fmt:
	$(GO) fmt ./...

smoke: build
	./bin/agx --help >/dev/null
	./bin/agx --version >/dev/null
	./bin/agx runtime --help >/dev/null
	./bin/agx doctor >/dev/null

smoke-desktop: desktop
	test -x bin/agx-desktop
	test -f desktop/frontend/dist/index.html
	test -n "$$(find desktop/frontend/dist/assets -name 'index-*.js' -print -quit)"

runtime-start: build
	./bin/agx runtime start

runtime-stop: build
	./bin/agx runtime stop

runtime-status: build
	./bin/agx runtime status

runtime-bg: build
	@mkdir -p bin/logs
	@./bin/agx runtime stop >/dev/null 2>&1 || true
	@for i in $$(seq 1 50); do \
		./bin/agx runtime status >/dev/null 2>&1 || break; \
		sleep 0.1; \
	done
	@nohup ./bin/agx runtime start >bin/logs/runtime.log 2>bin/logs/runtime.err.log & \
	pid=$$!; \
	ready=0; \
	for i in $$(seq 1 50); do \
		if ./bin/agx runtime status >/dev/null 2>&1; then \
			ready=$$((ready + 1)); \
			test $$ready -ge 2 && exit 0; \
		else \
			ready=0; \
		fi; \
		kill -0 $$pid >/dev/null 2>&1 || break; \
		sleep 0.1; \
	done; \
	echo "runtime did not start; see bin/logs/runtime.err.log" >&2; \
	test ! -s bin/logs/runtime.err.log || tail -40 bin/logs/runtime.err.log >&2; \
	exit 1

desktop-run: desktop
	./bin/agx-desktop

dev: app runtime-bg
	./bin/agx-desktop

service-install: build
	./bin/agx runtime install-service

service-uninstall: build
	./bin/agx runtime uninstall-service

doctor: build
	./bin/agx doctor
