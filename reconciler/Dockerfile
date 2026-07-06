# Cortex reconciler image.
# Build from the REPO ROOT so the shared module is in context:
#   docker build -f reconciler/Dockerfile -t cortex-reconciler:dev .

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY shared ./shared
COPY reconciler ./reconciler
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/reconciler ./reconciler/cmd/reconciler

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/reconciler /reconciler
USER nonroot:nonroot
ENTRYPOINT ["/reconciler"]
