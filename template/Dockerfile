FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
RUN apk add --no-cache git bash
COPY . .
RUN go install github.com/yogasw/wick@latest
RUN wick setup
# --headless drops the systray UI (container has no display) and
# --target picks up the docker buildx target platform so multi-arch
# images (linux/amd64, linux/arm64) build without extra config.
RUN wick build --headless --target ${TARGETOS}/${TARGETARCH} -o /out/app

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/app /app/app
EXPOSE 9425
ENTRYPOINT ["/app/app"]
CMD ["server"]
