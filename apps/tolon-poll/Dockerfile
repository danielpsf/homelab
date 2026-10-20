FROM golang:1.26.10-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tolon-poll .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tolon-poll /tolon-poll
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/tolon-poll"]
