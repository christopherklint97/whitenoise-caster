.PHONY: build run clean docker-build docker-up deploy

BINARY := whitenoise-caster

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY) -config config.yaml

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
