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

# The API compiles a deployment's Bicep → ARM and inspects Helm charts at save
# time, so the runtime image ships the pinned Bicep + Helm CLIs. Bicep's binary
# needs glibc + libicu, so this stage is debian-slim (not distroless). Non-root.
FROM debian:12-slim
ARG BICEP_VERSION=v0.30.23
ARG HELM_VERSION=v3.16.4
# ADD from a URL works in the classic (non-BuildKit) builder ACR uses; chmod/extract
# in a separate RUN so we don't depend on BuildKit's `ADD --chmod`. (ADD does not
# auto-extract remote tarballs, so helm is untarred explicitly.)
ADD https://github.com/Azure/bicep/releases/download/${BICEP_VERSION}/bicep-linux-x64 /usr/local/bin/bicep
ADD https://get.helm.sh/helm-${HELM_VERSION}-linux-amd64.tar.gz /tmp/helm.tgz
RUN chmod 0755 /usr/local/bin/bicep \
 && tar -xzf /tmp/helm.tgz -C /usr/local/bin --strip-components=1 linux-amd64/helm \
 && rm /tmp/helm.tgz \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates libicu72 \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -r -u 65532 -m -d /home/nonroot nonroot \
 && /usr/local/bin/bicep --version \
 && /usr/local/bin/helm version --short
COPY --from=build /out/api /api
USER 65532:65532
ENV HOME=/home/nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
