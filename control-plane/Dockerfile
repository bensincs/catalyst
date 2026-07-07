# Cortex control-plane API image.
# Build from the REPO ROOT so the shared module + go.mod are in context:
#   docker build -f control-plane/Dockerfile -t cortex-api:dev .
#   az acr build -r <acr> -t cortex-api:latest -f control-plane/Dockerfile .
#
# schema.sql / seed.sql are //go:embed-ed into the binary, so the runtime image
# needs nothing but the static binary. Config comes from container env vars
# (see control-plane/infra); there is no .env in the image.

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared ./shared
COPY control-plane ./control-plane
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/api ./control-plane/cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
