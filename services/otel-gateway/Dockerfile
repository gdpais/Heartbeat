FROM golang:1.26.0-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/otel-gateway ./services/otel-gateway/cmd/otel-gateway

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build /out/otel-gateway /app/otel-gateway

EXPOSE 8083

USER nonroot:nonroot
ENTRYPOINT ["/app/otel-gateway"]
