FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache curl make
COPY . .
RUN go mod download
RUN go install github.com/a-h/templ/cmd/templ@v0.2.747
RUN curl -L -o /usr/local/bin/tailwindcss \
    https://github.com/tailwindlabs/tailwindcss/releases/download/v3.4.0/tailwindcss-linux-x64 \
    && chmod +x /usr/local/bin/tailwindcss
RUN templ generate && \
    tailwindcss -i web/input.css -o static/output.css --minify && \
    go build -o /out/app .

FROM alpine:3.19
WORKDIR /app
COPY --from=build /out/app /app/app
EXPOSE 8080
ENTRYPOINT ["/app/app"]
CMD ["server"]
