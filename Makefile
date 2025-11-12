IMAGE ?= executor-demo/executor:demo

RUN_DOCKER = cmd /c scripts\\run-docker.cmd

.PHONY: all docker-build docker-run k8s-apply k8s-clean

all: docker-build

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	$(RUN_DOCKER)

k8s-apply:
	./scripts/run-k8s.sh

k8s-clean:
	kubectl delete -f k8s/job.yaml --ignore-not-found=true || true
