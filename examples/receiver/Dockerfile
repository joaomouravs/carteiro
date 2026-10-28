# syntax=docker/dockerfile:1

# Built from the repository root (see deploy/compose.yaml): the receiver's
# go.mod replaces the Carteiro module with ../.., so it must be in the context.
FROM golang:1.27.3-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
COPY examples/receiver/go.mod examples/receiver/go.sum examples/receiver/
RUN --mount=type=cache,target=/go/pkg/mod cd examples/receiver && go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd examples/receiver && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/receiver .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/receiver /receiver
EXPOSE 9000
ENTRYPOINT ["/receiver"]
