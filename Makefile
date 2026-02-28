.PHONY: build run dev clean docker-build docker-up docker-down deploy deploy-prod test test-integration vet
.PHONY: k8s-apply k8s-deploy k8s-status k8s-logs k8s-secret k8s-secret-pull

BINARY := whitenoise-caster
NAMESPACE := whitenoise

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY) -config config.yaml

dev:
	@which air > /dev/null 2>&1 || go install github.com/air-verse/air@latest
	air

clean:
	rm -f $(BINARY)

docker-build:
	docker build -t $(BINARY) .

docker-up:
	docker compose up -d

docker-down:
	docker compose down

deploy: docker-build
	docker compose up -d --build

deploy-prod:
	docker compose -f docker-compose.prod.yml up -d --pull always

test:
	go test ./... -count=1

test-integration:
	go test -tags integration -v -timeout 120s ./cast/

vet:
	go vet ./...

# --- k3s targets ---

k8s-apply:
	kubectl apply -f k8s/namespace.yaml
	kubectl apply -f k8s/cert-manager/clusterissuer.yaml
	kubectl apply -f k8s/service.yaml
	kubectl apply -f k8s/ingress.yaml
	kubectl apply -f k8s/deployment.yaml

k8s-deploy:
	kubectl rollout restart deployment/$(BINARY) -n $(NAMESPACE)
	kubectl rollout status deployment/$(BINARY) -n $(NAMESPACE) --timeout=120s

k8s-status:
	kubectl get pods,svc,ingress -n $(NAMESPACE)

k8s-logs:
	kubectl logs -f deployment/$(BINARY) -n $(NAMESPACE)

k8s-secret:
	kubectl create secret generic whitenoise-config \
		--from-file=config.yaml=config.prod.yaml \
		-n $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -

k8s-secret-pull:
	@test -n "$(GHCR_TOKEN)" || (echo "Usage: make k8s-secret-pull GHCR_TOKEN=<token> GHCR_USER=<user>"; exit 1)
	kubectl create secret docker-registry ghcr-secret \
		--docker-server=ghcr.io \
		--docker-username=$(GHCR_USER) \
		--docker-password=$(GHCR_TOKEN) \
		-n $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -
