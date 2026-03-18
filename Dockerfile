 FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
 WORKDIR /app

 COPY go.mod go.sum ./
 RUN go mod download

 COPY . .
 ARG TARGETOS TARGETARCH
 RUN --mount=type=cache,target=/root/.cache/go-build \
     --mount=type=cache,target=/go/pkg \
     CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o sitespeed-service ./cmd/api

 FROM alpine:latest
 RUN apk add --no-cache ca-certificates
 WORKDIR /app

 COPY --from=build /app/sitespeed-service .

 EXPOSE 8080

 ENTRYPOINT ["./sitespeed-service"]
