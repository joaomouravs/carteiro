# syntax=docker/dockerfile:1

# Build stage: the full Go toolchain, discarded after the build.
FROM golang:1.27.3-alpine AS build
WORKDIR /src

# Download modules in their own layer, so changing code does not invalidate it.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
# CGO_ENABLED=0 produces a static binary that needs no libc in the final image.
# -trimpath removes local paths from the binary; -s -w drops debug symbols.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/carteiro ./cmd/carteiro

# Runtime stage: distroless has CA certificates (needed for HTTPS deliveries)
# and time zone data, but no shell or package manager to exploit, and runs as
# an unprivileged user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/carteiro /carteiro
EXPOSE 8080
ENTRYPOINT ["/carteiro"]
CMD ["serve"]
