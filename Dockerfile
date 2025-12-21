 FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
 WORKDIR /app

 COPY go.mod go.sum ./
 RUN go mod download

 COPY . .
 ARG TARGETOS TARGETARCH
 RUN --mount=type=cache,target=/root/.cache/go-build \
     --mount=type=cache,target=/go/pkg \
     CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o sitespeed-service ./cmd/api

 FROM sitespeedio/sitespeed.io:latest
 WORKDIR /app

 COPY --from=build /app/sitespeed-service .

 ENV PORT=8080 \
     SITESPEED_BIN="/usr/src/app/bin/sitespeed.js"

 EXPOSE 8080

 ENTRYPOINT ["./sitespeed-service"]