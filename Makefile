.PHONY: build validate test package ci release

build:
	docker buildx build --target build-artifacts --output type=local,dest=. -f Dockerfile .

validate:
	docker buildx build --target validate-artifacts --output type=local,dest=. -f Dockerfile .

test:
	docker build -t upgrade-responder-build --target base -f Dockerfile .
	@docker rm -f upgrade-responder-test 2>/dev/null || true
	docker run --name upgrade-responder-test upgrade-responder-build ./scripts/test; \
		rc=$$?; \
		docker cp upgrade-responder-test:/go/src/github.com/longhorn/upgrade-responder/coverage.out . 2>/dev/null || true; \
		docker rm upgrade-responder-test 2>/dev/null || true; \
		exit $$rc

package: build
	./scripts/package

ci: build test validate package

release: ci

.DEFAULT_GOAL := ci
