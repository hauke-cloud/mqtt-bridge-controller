BINARY        := controller
IMAGE_TAG     ?= latest
IMAGE_REPO    ?= ghcr.io/hauke-cloud/mqtt-bridge-controller
CONTROLLER_GEN ?= go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.17.0
SETUP_ENVTEST  ?= go run sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: all build test lint fmt vet generate manifests setup-envtest docker-build docker-push install uninstall help

all: generate manifests fmt vet build test

## Build the controller binary
build:
	go build -trimpath -ldflags="-s -w" -o bin/$(BINARY) ./cmd/controller

## Run unit tests with race detector
test:
	go test -v -race -coverprofile=coverage.out -covermode=atomic ./...

## Run go vet
vet:
	go vet ./...

## Run gofmt
fmt:
	gofmt -s -l -w .

## Run golangci-lint
lint:
	golangci-lint run ./...

## Run go mod tidy
tidy:
	go mod tidy

## Generate DeepCopy methods
generate:
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

## Download envtest binaries (used by controller-runtime integration tests)
setup-envtest:
	$(SETUP_ENVTEST) use

## Generate CRD + RBAC manifests from kubebuilder markers
manifests:
	$(CONTROLLER_GEN) \
		crd \
		rbac:roleName=mqtt-bridge-controller-role \
		paths="./..." \
		output:crd:dir=config/crd/bases

## Install CRDs into the current cluster (kubectl must be configured)
install:
	kubectl apply -f config/crd/bases/

## Uninstall CRDs from the current cluster
uninstall:
	kubectl delete -f config/crd/bases/ --ignore-not-found

## Build Docker image
docker-build:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

## Push Docker image
docker-push:
	docker push $(IMAGE_REPO):$(IMAGE_TAG)

## Print help
help:
	@grep -E '^## ' Makefile | sed 's/## //'
