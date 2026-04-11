IMAGE ?= node-watchdog
TAG ?= latest
CHART_DIR := deploy/node-watchdog
RELEASE_NAME ?= node-watchdog
NAMESPACE ?= node-watchdog

.PHONY: build test docker-build docker-push deploy template uninstall clean

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/node-watchdog ./cmd/watchdog

test:
	go test -v -race ./...

docker-build:
	docker build -t $(IMAGE):$(TAG) .

docker-push:
	docker push $(IMAGE):$(TAG)

deploy:
	helm upgrade --install $(RELEASE_NAME) $(CHART_DIR) \
		--namespace $(NAMESPACE) --create-namespace \
		--set image.repository=$(IMAGE) \
		--set image.tag=$(TAG)

template:
	helm template $(RELEASE_NAME) $(CHART_DIR) --namespace $(NAMESPACE)

uninstall:
	helm uninstall $(RELEASE_NAME) --namespace $(NAMESPACE)

clean:
	rm -rf bin/
