FROM --platform=linux/amd64 node:22-alpine AS frontend

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY web/src/ web/src/
RUN npm run build

FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend /app/web/app.js web/app.js
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o whitenoise-caster .

FROM alpine:3.23

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/whitenoise-caster .

EXPOSE 8080

ENTRYPOINT ["./whitenoise-caster"]
CMD ["-config", "config.yaml"]
