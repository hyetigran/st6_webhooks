FROM golang:1.26-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Migrations are embedded via go:embed (internal/db/migrate.go), so unlike
# node/Dockerfile there's no separate "copy migrations into the image" step.
RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate
RUN CGO_ENABLED=0 go build -o /out/seed ./cmd/seed

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/api /out/migrate /out/seed ./
EXPOSE 8090
CMD ["./api"]
