# Build stage
FROM mcr.microsoft.com/dotnet/sdk:10.0 AS build
WORKDIR /src

# Install AOT dependencies
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       clang \
       zlib1g-dev

COPY sitespeed-service.csproj .
RUN dotnet restore

COPY . .
RUN --mount=type=cache,id=dotnet_tools,target=/root/.dotnet dotnet publish -c Release -r linux-arm64 /p:PublishAot=true -o /app/publish

# Runtime stage
FROM sitespeedio/sitespeed.io:latest
WORKDIR /app

COPY --from=build /app/publish/sitespeed-service .
COPY --from=build /app/publish/appsettings*.json .

ENV ASPNETCORE_URLS=http://+:3001 \
    SITESPEED_BIN="/usr/src/app/bin/sitespeed.js"

EXPOSE 3001

ENTRYPOINT ["./sitespeed-service"]
