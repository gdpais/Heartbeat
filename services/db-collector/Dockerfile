FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Cross-compile for the requested image platform using the native build stage.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/db-collector ./services/db-collector/cmd/db-collector

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build /out/db-collector /app/db-collector

EXPOSE 8082

USER nonroot:nonroot
ENTRYPOINT ["/app/db-collector"]
