FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/subscription-server .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/subscription-server /app/subscription-server
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/subscription-server"]
CMD ["--template", "/app/config/youknow.yaml"]
