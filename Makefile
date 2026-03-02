.PHONY: build run dev clean docker-build docker-up docker-down deploy deploy-prod test vet web-build web-test

BINARY := whitenoise-caster

web-build:
	npm run build

build: web-build
	go build -o $(BINARY) .

run: build
	./$(BINARY) -config config.yaml

dev:
	@which air > /dev/null 2>&1 || go install github.com/air-verse/air@latest
	air

clean:
	rm -f $(BINARY) web/app.js

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

test: web-build
	go test ./... -count=1

web-test:
	npm test

vet:
	go vet ./...
