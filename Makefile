.PHONY: build run dev clean docker-build docker-up deploy test vet

BINARY := whitenoise-caster

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

test:
	go test ./... -count=1

vet:
	go vet ./...
